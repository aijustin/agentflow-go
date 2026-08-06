package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	schemamigrations "github.com/aijustin/agentflow-go/migrations/postgres"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const DefaultTableName = "agentflow_run_snapshots"

// DefaultOutboxTableName is the transactional event outbox written by
// SaveWithEvents and drained by the framework relay (migration 0005).
const DefaultOutboxTableName = "agentflow_outbox"

// DefaultEventTableName is the durable runtime-event table. SaveWithEvents
// reads its per-run max sequence (under the same advisory lock the event
// store's Append takes) so outbox rows continue the run's sequence without
// colliding with directly appended events.
const DefaultEventTableName = "agentflow_runtime_events"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Option func(*Repository) error

type Repository struct {
	db          *sql.DB
	table       string
	outboxTable string
	eventTable  string
}

func NewRepository(db *sql.DB, opts ...Option) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres runstate: db is nil")
	}
	repo := &Repository{db: db, table: DefaultTableName, outboxTable: DefaultOutboxTableName, eventTable: DefaultEventTableName}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(repo); err != nil {
			return nil, err
		}
	}
	return repo, nil
}

func WithTableName(name string) Option {
	return func(repo *Repository) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres runstate: invalid table name %q", name)
		}
		repo.table = name
		return nil
	}
}

// WithOutboxTableName overrides the event outbox table. It must match the
// table the framework relay and the observability outbox sink use.
func WithOutboxTableName(name string) Option {
	return func(repo *Repository) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres runstate: invalid outbox table name %q", name)
		}
		repo.outboxTable = name
		return nil
	}
}

// WithEventTableName tells SaveWithEvents which runtime-event table sequences
// must stay compatible with. It must match the observability event store's
// table; the default is the store's own default.
func WithEventTableName(name string) Option {
	return func(repo *Repository) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres runstate: invalid event table name %q", name)
		}
		repo.eventTable = name
		return nil
	}
}

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	stored, data, threadID, err := r.prepareSave(ctx, snapshot, expectedVersion)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		query := fmt.Sprintf(`INSERT INTO %s (run_id, version, scenario_name, status, current_node_id, parent_run_id, thread_id, fork_from_version, snapshot_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (run_id) DO NOTHING`, r.table)
		result, err := r.db.ExecContext(ctx, query, stored.RunID, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data)
		if err != nil {
			return fmt.Errorf("postgres runstate: insert snapshot %q: %w", stored.RunID, err)
		}
		if err := requireAffected(result, 1); err != nil {
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				return err
			}
			return fmt.Errorf("postgres runstate: insert snapshot %q: %w", stored.RunID, err)
		}
		snapshot.Version = stored.Version
		return nil
	}
	query := fmt.Sprintf(`UPDATE %s
SET version = $1, scenario_name = $2, status = $3, current_node_id = $4, parent_run_id = $5, thread_id = $6, fork_from_version = $7, snapshot_json = $8, updated_at = NOW()
WHERE run_id = $9 AND version = $10`, r.table)
	result, err := r.db.ExecContext(ctx, query, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, stored.RunID, expectedVersion)
	if err != nil {
		return fmt.Errorf("postgres runstate: update snapshot %q: %w", stored.RunID, err)
	}
	if err := requireAffected(result, 1); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			return err
		}
		return fmt.Errorf("postgres runstate: update snapshot %q: %w", stored.RunID, err)
	}
	snapshot.Version = stored.Version
	return nil
}

// SaveFenced implements runstate.FencedRepository. The update carries
// fence_token <= $token in its WHERE clause, atomically with the version CAS,
// so a superseded lease holder cannot overwrite a newer holder's snapshot
// even if it still passes the version check. When no row matches, a follow-up
// read distinguishes the cause: version moved -> ErrStaleSnapshot; a higher
// fence token is recorded -> ErrStaleFence.
func (r *Repository) SaveFenced(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64, fenceToken uint64) error {
	stored, data, threadID, err := r.prepareSave(ctx, snapshot, expectedVersion)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		query := fmt.Sprintf(`INSERT INTO %s (run_id, version, scenario_name, status, current_node_id, parent_run_id, thread_id, fork_from_version, snapshot_json, fence_token, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
ON CONFLICT (run_id) DO NOTHING`, r.table)
		result, err := r.db.ExecContext(ctx, query, stored.RunID, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, int64(fenceToken))
		if err != nil {
			return fmt.Errorf("postgres runstate: insert fenced snapshot %q: %w", stored.RunID, err)
		}
		if err := requireAffected(result, 1); err != nil {
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				return r.classifyStaleSave(ctx, stored.RunID, expectedVersion, fenceToken)
			}
			return fmt.Errorf("postgres runstate: insert fenced snapshot %q: %w", stored.RunID, err)
		}
		snapshot.Version = stored.Version
		return nil
	}
	query := fmt.Sprintf(`UPDATE %s
SET version = $1, scenario_name = $2, status = $3, current_node_id = $4, parent_run_id = $5, thread_id = $6, fork_from_version = $7, snapshot_json = $8, fence_token = $11, updated_at = NOW()
WHERE run_id = $9 AND version = $10 AND fence_token <= $11`, r.table)
	result, err := r.db.ExecContext(ctx, query, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, stored.RunID, expectedVersion, int64(fenceToken))
	if err != nil {
		return fmt.Errorf("postgres runstate: update fenced snapshot %q: %w", stored.RunID, err)
	}
	if err := requireAffected(result, 1); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			return r.classifyStaleSave(ctx, stored.RunID, expectedVersion, fenceToken)
		}
		return fmt.Errorf("postgres runstate: update fenced snapshot %q: %w", stored.RunID, err)
	}
	snapshot.Version = stored.Version
	return nil
}

// classifyStaleSave determines why a fenced save matched no rows: the run is
// gone, the version moved underneath the caller, or a newer fence token is
// recorded (a newer lease holder already wrote). Version is checked first so
// a caller that is simply out of date still sees ErrStaleSnapshot.
func (r *Repository) classifyStaleSave(ctx context.Context, runID string, expectedVersion int64, fenceToken uint64) error {
	query := fmt.Sprintf(`SELECT version, fence_token FROM %s WHERE run_id = $1`, r.table)
	var version int64
	var storedFence uint64
	if err := r.db.QueryRowContext(ctx, query, runID).Scan(&version, &storedFence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runstate.ErrNotFound
		}
		return fmt.Errorf("postgres runstate: classify stale save %q: %w", runID, err)
	}
	if version != expectedVersion {
		return runstate.ErrStaleSnapshot
	}
	if storedFence > fenceToken {
		return runstate.ErrStaleFence
	}
	// Neither version nor fence explains the miss; report the conservative
	// error so the caller retries through the stale-snapshot path.
	return runstate.ErrStaleSnapshot
}

// prepareSave runs the shared validation, status-transition check, timestamp
// stamping, version bump, and marshaling for Save and SaveFenced.
func (r *Repository) prepareSave(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) (runstate.RunSnapshot, []byte, string, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, nil, "", err
	}
	if snapshot == nil {
		return runstate.RunSnapshot{}, nil, "", runstate.ErrNotFound
	}
	var previous *runstate.RunSnapshot
	if expectedVersion > 0 {
		prev, loadErr := r.Load(ctx, snapshot.RunID)
		if loadErr != nil {
			return runstate.RunSnapshot{}, nil, "", loadErr
		}
		previous = &prev
	}
	return buildSave(ctx, snapshot, previous, expectedVersion)
}

// buildSave is the pure part of prepareSave: validation, transition check,
// stamping, version bump, and marshaling, given the already-loaded previous
// snapshot (nil for a create). SaveWithEvents reuses it with a previous
// loaded inside its own transaction.
func buildSave(ctx context.Context, snapshot *runstate.RunSnapshot, previous *runstate.RunSnapshot, expectedVersion int64) (runstate.RunSnapshot, []byte, string, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, nil, "", err
	}
	if snapshot == nil {
		return runstate.RunSnapshot{}, nil, "", runstate.ErrNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return runstate.RunSnapshot{}, nil, "", err
	}
	if err := runstate.ValidateStatusTransition(ctx, previous, snapshot.Status); err != nil {
		return runstate.RunSnapshot{}, nil, "", err
	}
	runstate.StampSnapshot(snapshot, previous, time.Now().UTC())
	next := snapshot.Version
	if next <= expectedVersion {
		next = expectedVersion + 1
	}
	stored := *snapshot
	stored.Version = next
	data, err := json.Marshal(stored)
	if err != nil {
		return runstate.RunSnapshot{}, nil, "", fmt.Errorf("postgres runstate: marshal snapshot %q: %w", snapshot.RunID, err)
	}
	return stored, data, runstate.IndexedThreadID(stored), nil
}

func (r *Repository) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	query := fmt.Sprintf(`SELECT snapshot_json FROM %s WHERE run_id = $1`, r.table)
	var data []byte
	if err := r.db.QueryRowContext(ctx, query, runID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runstate.RunSnapshot{}, runstate.ErrNotFound
		}
		return runstate.RunSnapshot{}, fmt.Errorf("postgres runstate: load snapshot %q: %w", runID, err)
	}
	var snapshot runstate.RunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("postgres runstate: decode snapshot %q: %w", runID, err)
	}
	if err := snapshot.Validate(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	return snapshot, nil
}

func (r *Repository) Delete(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE run_id = $1`, r.table)
	if _, err := r.db.ExecContext(ctx, query, runID); err != nil {
		return fmt.Errorf("postgres runstate: delete snapshot %q: %w", runID, err)
	}
	return nil
}

// SchemaVersion reports the highest applied agentflow schema migration
// version visible to this repository's database. A database without the
// version table reports 0 (never migrated) instead of an error, so startup
// validation can demand migrations with a clear message.
func (r *Repository) SchemaVersion(ctx context.Context) (int, error) {
	return schemamigrations.AppliedVersion(ctx, r.db)
}

func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query, args := r.buildListQuery(filter, nil)
	return r.querySnapshots(ctx, query, args)
}

// ListStale implements runstate.StaleRepository. The staleness comparison
// runs in SQL against NOW(), the database clock, so application-clock skew on
// any worker cannot shrink or stretch the grace window.
func (r *Repository) ListStale(ctx context.Context, filter runstate.ListFilter, grace time.Duration) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if grace < 0 {
		grace = 0
	}
	graceSeconds := grace.Seconds()
	query, args := r.buildListQuery(filter, &graceSeconds)
	return r.querySnapshots(ctx, query, args)
}

// buildListQuery assembles the SELECT for List and ListStale. When
// graceSeconds is non-nil, an extra clause keeps only rows whose updated_at
// is at least that many seconds in the past by the database clock.
func (r *Repository) buildListQuery(filter runstate.ListFilter, graceSeconds *float64) (string, []any) {
	args := []any{}
	where := ""
	addClause := func(clause string) {
		if where == "" {
			where = " WHERE " + clause
		} else {
			where += " AND " + clause
		}
	}
	if filter.Status != "" {
		args = append(args, string(filter.Status))
		addClause(fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.ScenarioName != "" {
		args = append(args, filter.ScenarioName)
		addClause(fmt.Sprintf("scenario_name = $%d", len(args)))
	}
	if filter.ParentRunID != "" {
		args = append(args, filter.ParentRunID)
		addClause(fmt.Sprintf("parent_run_id = $%d", len(args)))
	}
	if filter.ThreadID != "" {
		args = append(args, filter.ThreadID)
		addClause(fmt.Sprintf("COALESCE(NULLIF(thread_id, ''), run_id) = $%d", len(args)))
	}
	if filter.TenantID != "" {
		args = append(args, filter.TenantID)
		addClause(fmt.Sprintf("snapshot_json->>'tenant_id' = $%d", len(args)))
	}
	if !filter.UpdatedBefore.IsZero() {
		args = append(args, filter.UpdatedBefore.UTC())
		addClause(fmt.Sprintf("updated_at < $%d", len(args)))
	}
	if graceSeconds != nil {
		args = append(args, *graceSeconds)
		addClause(fmt.Sprintf("updated_at < NOW() - make_interval(secs => $%d)", len(args)))
	}
	limit := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limit = fmt.Sprintf(" LIMIT $%d", len(args))
	}
	return fmt.Sprintf(`SELECT snapshot_json FROM %s%s%s`, r.table, where, limit), args
}

func (r *Repository) querySnapshots(ctx context.Context, query string, args []any) ([]runstate.RunSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres runstate: list snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []runstate.RunSnapshot
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("postgres runstate: scan snapshot: %w", err)
		}
		var snap runstate.RunSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("postgres runstate: decode snapshot: %w", err)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func requireAffected(result sql.Result, want int64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != want {
		return runstate.ErrStaleSnapshot
	}
	return nil
}

func validTableName(name string) bool {
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !identifierPattern.MatchString(part) {
			return false
		}
	}
	return true
}
