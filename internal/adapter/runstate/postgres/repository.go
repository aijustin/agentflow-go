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

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const DefaultTableName = "agentflow_run_snapshots"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Option func(*Repository) error

type Repository struct {
	db    *sql.DB
	table string
}

func NewRepository(db *sql.DB, opts ...Option) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres runstate: db is nil")
	}
	repo := &Repository{db: db, table: DefaultTableName}
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

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	var previous *runstate.RunSnapshot
	if expectedVersion > 0 {
		prev, loadErr := r.Load(ctx, snapshot.RunID)
		if loadErr != nil {
			return loadErr
		}
		previous = &prev
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
		return fmt.Errorf("postgres runstate: marshal snapshot %q: %w", snapshot.RunID, err)
	}
	threadID := runstate.IndexedThreadID(stored)
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
		snapshot.Version = next
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
	snapshot.Version = next
	return nil
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

func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args := []any{}
	where := ""
	if filter.Status != "" {
		args = append(args, string(filter.Status))
		where += fmt.Sprintf(" WHERE status = $%d", len(args))
	}
	if filter.ScenarioName != "" {
		args = append(args, filter.ScenarioName)
		if where == "" {
			where = fmt.Sprintf(" WHERE scenario_name = $%d", len(args))
		} else {
			where += fmt.Sprintf(" AND scenario_name = $%d", len(args))
		}
	}
	if filter.ParentRunID != "" {
		args = append(args, filter.ParentRunID)
		if where == "" {
			where = fmt.Sprintf(" WHERE parent_run_id = $%d", len(args))
		} else {
			where += fmt.Sprintf(" AND parent_run_id = $%d", len(args))
		}
	}
	if filter.ThreadID != "" {
		args = append(args, filter.ThreadID)
		if where == "" {
			where = fmt.Sprintf(" WHERE COALESCE(NULLIF(thread_id, ''), run_id) = $%d", len(args))
		} else {
			where += fmt.Sprintf(" AND COALESCE(NULLIF(thread_id, ''), run_id) = $%d", len(args))
		}
	}
	limit := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limit = fmt.Sprintf(" LIMIT $%d", len(args))
	}
	query := fmt.Sprintf(`SELECT snapshot_json FROM %s%s%s`, r.table, where, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres runstate: list snapshots: %w", err)
	}
	defer rows.Close()
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
		if filter.TenantID != "" && snap.TenantID != filter.TenantID {
			continue
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
