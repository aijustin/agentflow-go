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

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

const DefaultTableName = "agentflow_runtime_events"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Config struct {
	DB              *sql.DB
	TableName       string
	SkipSchemaSetup bool
}

type Store struct {
	db    *sql.DB
	table string
	now   func() time.Time
}

func NewStore(ctx context.Context, config Config) (*Store, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("postgres observability: db is nil")
	}
	table := config.TableName
	if table == "" {
		table = DefaultTableName
	}
	if !validTableName(table) {
		return nil, fmt.Errorf("postgres observability: invalid table name %q", table)
	}
	store := &Store{db: config.DB, table: table, now: func() time.Time { return time.Now().UTC() }}
	if !config.SkipSchemaSetup {
		if err := store.EnsureSchema(ctx); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) EnsureSchema(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	indexPrefix := store.indexPrefix()
	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	id bigserial PRIMARY KEY,
	run_id text NOT NULL,
	tenant_id text NOT NULL DEFAULT '',
	sequence bigint NOT NULL,
	event_type text NOT NULL,
	scenario_name text NOT NULL DEFAULT '',
	trace_id text NOT NULL DEFAULT '',
	span_id text NOT NULL DEFAULT '',
	occurred_at timestamptz NOT NULL,
	payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (run_id, sequence)
)`, store.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_run_sequence_idx
ON %s (run_id, sequence)`, indexPrefix, store.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_run_updated_idx
ON %s (run_id, occurred_at DESC)`, indexPrefix, store.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_type_time_idx
ON %s (event_type, occurred_at DESC)`, indexPrefix, store.table),
	}
	for _, query := range queries {
		if _, err := store.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("postgres observability: ensure schema: %w", err)
		}
	}
	for _, column := range []string{
		`tenant_id text NOT NULL DEFAULT ''`,
		`parent_span_id text NOT NULL DEFAULT ''`,
		`episode_id text NOT NULL DEFAULT ''`,
		`session_id text NOT NULL DEFAULT ''`,
		`trigger_kind text NOT NULL DEFAULT ''`,
	} {
		if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s`, store.table, column)); err != nil {
			return fmt.Errorf("postgres observability: ensure column %s: %w", column, err)
		}
	}
	for _, index := range []string{
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_tenant_run_idx ON %s (tenant_id, run_id)`, indexPrefix, store.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_episode_id_idx ON %s (episode_id, id)`, indexPrefix, store.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_session_id_idx ON %s (session_id, id)`, indexPrefix, store.table),
	} {
		if _, err := store.db.ExecContext(ctx, index); err != nil {
			return fmt.Errorf("postgres observability: ensure scoped index: %w", err)
		}
	}
	return nil
}

func (store *Store) Append(ctx context.Context, event core.Event) (obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return obspkg.EventRecord{}, err
	}
	if event.RunID == "" {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: run id is required")
	}
	obspkg.StampEventTenant(ctx, &event)
	event = obspkg.NormalizeEvent(event, store.now())
	payload := []byte(event.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, event.RunID); err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: lock run %q: %w", event.RunID, err)
	}
	var sequence int64
	sequenceQuery := fmt.Sprintf(`SELECT COALESCE(MAX(sequence), 0) + 1 FROM %s WHERE run_id = $1`, store.table)
	if err := tx.QueryRowContext(ctx, sequenceQuery, event.RunID).Scan(&sequence); err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: next sequence for run %q: %w", event.RunID, err)
	}
	insertQuery := fmt.Sprintf(`INSERT INTO %s (run_id, tenant_id, sequence, event_type, scenario_name, episode_id, session_id, trigger_kind, trace_id, span_id, parent_span_id, occurred_at, payload_json)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, created_at`, store.table)
	var id int64
	var createdAt time.Time
	if err := tx.QueryRowContext(ctx, insertQuery, event.RunID, event.TenantID, sequence, string(event.Type), event.ScenarioName, event.EpisodeID, event.SessionID, event.TriggerKind, event.TraceID, event.SpanID, event.ParentSpanID, event.Timestamp, payload).Scan(&id, &createdAt); err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: append event for run %q: %w", event.RunID, err)
	}
	if err := tx.Commit(); err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: commit append for run %q: %w", event.RunID, err)
	}
	return obspkg.EventRecord{ID: id, Sequence: sequence, Event: event, CreatedAt: createdAt.UTC()}, nil
}

// AppendSequenced implements observability.SequencedEventStore for the
// framework outbox relay: the event is inserted with a sequence minted
// earlier (when it was parked in the outbox), continuing the run's regular
// sequence space. A row that already exists at (run_id, sequence) means the
// event was delivered before — by an earlier relay attempt that crashed
// before marking, or by a concurrent relay — so it is read back and returned
// with a nil error instead of being duplicated.
func (store *Store) AppendSequenced(ctx context.Context, sequence int64, event core.Event) (obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return obspkg.EventRecord{}, err
	}
	if event.RunID == "" {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: run id is required")
	}
	if sequence <= 0 {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: sequence must be positive, got %d", sequence)
	}
	obspkg.StampEventTenant(ctx, &event)
	event = obspkg.NormalizeEvent(event, store.now())
	payload := []byte(event.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	insertQuery := fmt.Sprintf(`INSERT INTO %s (run_id, tenant_id, sequence, event_type, scenario_name, episode_id, session_id, trigger_kind, trace_id, span_id, parent_span_id, occurred_at, payload_json)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (run_id, sequence) DO NOTHING
RETURNING id, created_at`, store.table)
	var id int64
	var createdAt time.Time
	err := store.db.QueryRowContext(ctx, insertQuery, event.RunID, event.TenantID, sequence, string(event.Type), event.ScenarioName, event.EpisodeID, event.SessionID, event.TriggerKind, event.TraceID, event.SpanID, event.ParentSpanID, event.Timestamp, payload).Scan(&id, &createdAt)
	if err == nil {
		return obspkg.EventRecord{ID: id, Sequence: sequence, Event: event, CreatedAt: createdAt.UTC()}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: sequenced append for run %q: %w", event.RunID, err)
	}
	// Conflict: the event was already delivered. Return the stored row so the
	// relay can treat this as success and mark the outbox row published.
	selectQuery := fmt.Sprintf(`SELECT id, created_at FROM %s WHERE run_id = $1 AND sequence = $2`, store.table)
	if err := store.db.QueryRowContext(ctx, selectQuery, event.RunID, sequence).Scan(&id, &createdAt); err != nil {
		return obspkg.EventRecord{}, fmt.Errorf("postgres observability: read conflicting event for run %q sequence %d: %w", event.RunID, sequence, err)
	}
	return obspkg.EventRecord{ID: id, Sequence: sequence, Event: event, CreatedAt: createdAt.UTC()}, nil
}

// DeleteEventsForRun implements observability.EventRunPurger for the
// retention cascade: when a run is deleted, its event history goes with it.
func (store *Store) DeleteEventsForRun(ctx context.Context, runID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tenantID := obspkg.TenantIDFromContext(ctx)
	query := fmt.Sprintf(`DELETE FROM %s WHERE run_id = $1 AND ($2 = '' OR tenant_id = $2)`, store.table)
	result, err := store.db.ExecContext(ctx, query, runID, tenantID)
	if err != nil {
		return 0, fmt.Errorf("postgres observability: delete events for run %q: %w", runID, err)
	}
	return result.RowsAffected()
}

// PurgeEventsBefore implements observability.EventStorePurger: removes events
// that occurred before cutoff, bounding the append-only event table. Note
// this also trims history of runs still alive past cutoff; callers pick the
// cutoff to match their run retention.
func (store *Store) PurgeEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tenantID := obspkg.TenantIDFromContext(ctx)
	query := fmt.Sprintf(`DELETE FROM %s WHERE occurred_at < $1 AND ($2 = '' OR tenant_id = $2)`, store.table)
	result, err := store.db.ExecContext(ctx, query, cutoff.UTC(), tenantID)
	if err != nil {
		return 0, fmt.Errorf("postgres observability: purge events: %w", err)
	}
	return result.RowsAffected()
}

func (store *Store) ListRuns(ctx context.Context, query obspkg.RunQuery) ([]obspkg.RunSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeRunQuery(query)
	listQuery := fmt.Sprintf(`WITH summarized AS (
	SELECT
		run_id,
		tenant_id,
		COALESCE((array_agg(NULLIF(scenario_name, '') ORDER BY sequence DESC) FILTER (WHERE scenario_name <> ''))[1], '') AS scenario_name,
		CASE (array_agg(event_type ORDER BY sequence DESC))[1]
			WHEN 'RunCompleted' THEN 'completed'
			WHEN 'RunFailed' THEN 'failed'
			WHEN 'RunCancelled' THEN 'cancelled'
			WHEN 'RunPaused' THEN 'paused'
			ELSE 'running'
		END AS status,
		COUNT(*) AS event_count,
		MIN(occurred_at) AS first_seen_at,
		MAX(occurred_at) AS last_seen_at,
		(array_agg(event_type ORDER BY sequence DESC))[1] AS last_event_type
	FROM %s
	WHERE ($1 = '' OR tenant_id = $1)
	GROUP BY run_id, tenant_id
)
SELECT run_id, tenant_id, scenario_name, status, event_count, first_seen_at, last_seen_at, last_event_type
FROM summarized
WHERE ($2 = '' OR status = $2)
ORDER BY last_seen_at DESC, run_id ASC
LIMIT $3 OFFSET $4`, store.table)
	rows, err := store.db.QueryContext(ctx, listQuery, query.TenantID, string(query.Status), query.Limit, query.Offset)
	if err != nil {
		return nil, fmt.Errorf("postgres observability: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	runs := make([]obspkg.RunSummary, 0)
	for rows.Next() {
		var summary obspkg.RunSummary
		var status string
		var eventType string
		if err := rows.Scan(&summary.RunID, &summary.TenantID, &summary.ScenarioName, &status, &summary.EventCount, &summary.FirstSeenAt, &summary.LastSeenAt, &eventType); err != nil {
			return nil, fmt.Errorf("postgres observability: scan run: %w", err)
		}
		summary.Status = obspkg.RunStatus(status)
		summary.LastEventType = core.EventType(eventType)
		runs = append(runs, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres observability: list runs rows: %w", err)
	}
	return runs, nil
}

func (store *Store) ListEvents(ctx context.Context, runID string, query obspkg.EventQuery) ([]obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeEventQuery(query)
	if query.Preset == core.EventFilterDiagnostic {
		return store.listEventsRaw(ctx, query.TenantID, runID, query.AfterSequence, query.Limit)
	}
	// Preset projection: page raw rows until Limit matching events are collected.
	out := make([]obspkg.EventRecord, 0, query.Limit)
	after := query.AfterSequence
	for len(out) < query.Limit {
		batch, err := store.listEventsRaw(ctx, query.TenantID, runID, after, obspkg.MaxEventQueryLimit)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, record := range batch {
			after = record.Sequence
			if !obspkg.EventAllowedByPreset(record.Event.Type, query.Preset) {
				continue
			}
			out = append(out, record)
			if len(out) >= query.Limit {
				break
			}
		}
		if len(batch) < obspkg.MaxEventQueryLimit {
			break
		}
	}
	return out, nil
}

func (store *Store) listEventsRaw(ctx context.Context, tenantID, runID string, afterSequence int64, limit int) ([]obspkg.EventRecord, error) {
	listQuery := fmt.Sprintf(`SELECT id, sequence, event_type, run_id, tenant_id, scenario_name, episode_id, session_id, trigger_kind, trace_id, span_id, parent_span_id, occurred_at, payload_json, created_at
FROM %s
WHERE run_id = $1 AND sequence > $2 AND ($3 = '' OR tenant_id = $3)
ORDER BY sequence ASC
LIMIT $4`, store.table)
	rows, err := store.db.QueryContext(ctx, listQuery, runID, afterSequence, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres observability: list events for run %q: %w", runID, err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]obspkg.EventRecord, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres observability: scan event for run %q: %w", runID, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres observability: list events rows for run %q: %w", runID, err)
	}
	return records, nil
}

func (store *Store) ListScopedEvents(ctx context.Context, query obspkg.ScopedEventQuery) ([]obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeScopedEventQuery(query)
	if query.EpisodeID == "" && query.SessionID == "" {
		return nil, fmt.Errorf("postgres observability: episode_id or session_id is required")
	}
	if query.EpisodeID != "" && query.SessionID != "" {
		return nil, fmt.Errorf("postgres observability: episode_id and session_id are mutually exclusive")
	}
	scopeColumn := "episode_id"
	scopeValue := query.EpisodeID
	if query.SessionID != "" {
		scopeColumn = "session_id"
		scopeValue = query.SessionID
	}
	if query.Preset == core.EventFilterDiagnostic {
		return store.listScopedEventsRaw(ctx, query.TenantID, scopeColumn, scopeValue, query.AfterID, query.Limit)
	}
	out := make([]obspkg.EventRecord, 0, query.Limit)
	after := query.AfterID
	for len(out) < query.Limit {
		batch, err := store.listScopedEventsRaw(ctx, query.TenantID, scopeColumn, scopeValue, after, obspkg.MaxEventQueryLimit)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, record := range batch {
			after = record.ID
			if !obspkg.EventAllowedByPreset(record.Event.Type, query.Preset) {
				continue
			}
			out = append(out, record)
			if len(out) >= query.Limit {
				break
			}
		}
		if len(batch) < obspkg.MaxEventQueryLimit {
			break
		}
	}
	return out, nil
}

func (store *Store) listScopedEventsRaw(ctx context.Context, tenantID, scopeColumn, scopeValue string, afterID int64, limit int) ([]obspkg.EventRecord, error) {
	listQuery := fmt.Sprintf(`SELECT id, sequence, event_type, run_id, tenant_id, scenario_name, episode_id, session_id, trigger_kind, trace_id, span_id, parent_span_id, occurred_at, payload_json, created_at
FROM %s
WHERE %s = $1 AND id > $2 AND ($3 = '' OR tenant_id = $3)
ORDER BY id ASC
LIMIT $4`, store.table, scopeColumn)
	rows, err := store.db.QueryContext(ctx, listQuery, scopeValue, afterID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres observability: list scoped events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]obspkg.EventRecord, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres observability: scan scoped event: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres observability: list scoped events rows: %w", err)
	}
	return records, nil
}

func scanRecord(row interface{ Scan(dest ...any) error }) (obspkg.EventRecord, error) {
	var record obspkg.EventRecord
	var eventType string
	var payload []byte
	if err := row.Scan(
		&record.ID,
		&record.Sequence,
		&eventType,
		&record.Event.RunID,
		&record.Event.TenantID,
		&record.Event.ScenarioName,
		&record.Event.EpisodeID,
		&record.Event.SessionID,
		&record.Event.TriggerKind,
		&record.Event.TraceID,
		&record.Event.SpanID,
		&record.Event.ParentSpanID,
		&record.Event.Timestamp,
		&payload,
		&record.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return obspkg.EventRecord{}, err
		}
		return obspkg.EventRecord{}, err
	}
	record.Event.Type = core.EventType(eventType)
	if len(payload) > 0 && !json.Valid(payload) {
		return obspkg.EventRecord{}, fmt.Errorf("invalid payload json")
	}
	record.Event.Payload = obspkg.CloneRawMessage(payload)
	record.Event.Timestamp = record.Event.Timestamp.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	return record, nil
}

func (store *Store) indexPrefix() string {
	return strings.ReplaceAll(store.table, ".", "_")
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
