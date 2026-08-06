package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// SaveWithEvents implements runstate.EventOutboxRepository: the snapshot save
// (version CAS plus, for a non-zero fenceToken, the same fence validation as
// SaveFenced) and the outbox inserts commit in ONE transaction, so a run's
// state and its parked lifecycle events either both land or both roll back —
// a fence conflict or stale version leaves no orphan outbox rows behind.
//
// Each event's per-run sequence is minted inside the transaction under the
// same per-run advisory lock the observability event store's Append takes,
// continuing from the max sequence present in the event table and in
// still-unpublished outbox rows. The relay later inserts with exactly this
// sequence, so the event store's UNIQUE (run_id, sequence) constraint
// deduplicates redelivery.
func (r *Repository) SaveWithEvents(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64, events []core.Event, fenceToken uint64) error {
	if len(events) == 0 {
		if fenceToken == 0 {
			return r.Save(ctx, snapshot, expectedVersion)
		}
		return r.SaveFenced(ctx, snapshot, expectedVersion, fenceToken)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres runstate: begin save-with-events %q: %w", snapshot.RunID, err)
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize against direct event-store appends and other outbox writers
	// for this run so sequence minting below cannot collide.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, snapshot.RunID); err != nil {
		return fmt.Errorf("postgres runstate: lock run %q: %w", snapshot.RunID, err)
	}
	var previous *runstate.RunSnapshot
	if expectedVersion > 0 {
		prev, loadErr := loadSnapshotTx(ctx, tx, r.table, snapshot.RunID)
		if loadErr != nil {
			return loadErr
		}
		previous = &prev
	}
	stored, data, threadID, err := buildSave(ctx, snapshot, previous, expectedVersion)
	if err != nil {
		return err
	}
	if err := r.saveSnapshotTx(ctx, tx, stored, data, threadID, expectedVersion, fenceToken); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			_ = tx.Rollback()
			return r.classifyStaleSave(ctx, stored.RunID, expectedVersion, fenceToken)
		}
		return err
	}
	base, err := r.maxEventSequenceTx(ctx, tx, stored.RunID)
	if err != nil {
		return err
	}
	insertQuery := fmt.Sprintf(`INSERT INTO %s (run_id, sequence, event_type, scenario_name, payload_json)
VALUES ($1, $2, $3, $4, $5)`, r.outboxTable)
	for i, event := range events {
		payload, err := marshalOutboxEvent(event)
		if err != nil {
			return fmt.Errorf("postgres runstate: marshal outbox event for run %q: %w", stored.RunID, err)
		}
		if _, err := tx.ExecContext(ctx, insertQuery, stored.RunID, base+int64(i)+1, string(event.Type), event.ScenarioName, payload); err != nil {
			return fmt.Errorf("postgres runstate: insert outbox event for run %q: %w", stored.RunID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres runstate: commit save-with-events %q: %w", stored.RunID, err)
	}
	snapshot.Version = stored.Version
	return nil
}

// saveSnapshotTx runs the INSERT (expectedVersion == 0) or CAS UPDATE of the
// snapshot row inside tx. fenceToken == 0 uses the plain Save semantics; a
// non-zero token adds the fence_token <= token guard of SaveFenced.
func (r *Repository) saveSnapshotTx(ctx context.Context, tx *sql.Tx, stored runstate.RunSnapshot, data []byte, threadID string, expectedVersion int64, fenceToken uint64) error {
	if expectedVersion == 0 {
		var query string
		var args []any
		if fenceToken == 0 {
			query = fmt.Sprintf(`INSERT INTO %s (run_id, version, scenario_name, status, current_node_id, parent_run_id, thread_id, fork_from_version, snapshot_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (run_id) DO NOTHING`, r.table)
			args = []any{stored.RunID, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data}
		} else {
			query = fmt.Sprintf(`INSERT INTO %s (run_id, version, scenario_name, status, current_node_id, parent_run_id, thread_id, fork_from_version, snapshot_json, fence_token, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
ON CONFLICT (run_id) DO NOTHING`, r.table)
			args = []any{stored.RunID, stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, int64(fenceToken)}
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("postgres runstate: insert snapshot %q: %w", stored.RunID, err)
		}
		return requireAffected(result, 1)
	}
	var query string
	var args []any
	if fenceToken == 0 {
		query = fmt.Sprintf(`UPDATE %s
SET version = $1, scenario_name = $2, status = $3, current_node_id = $4, parent_run_id = $5, thread_id = $6, fork_from_version = $7, snapshot_json = $8, updated_at = NOW()
WHERE run_id = $9 AND version = $10`, r.table)
		args = []any{stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, stored.RunID, expectedVersion}
	} else {
		query = fmt.Sprintf(`UPDATE %s
SET version = $1, scenario_name = $2, status = $3, current_node_id = $4, parent_run_id = $5, thread_id = $6, fork_from_version = $7, snapshot_json = $8, fence_token = $11, updated_at = NOW()
WHERE run_id = $9 AND version = $10 AND fence_token <= $11`, r.table)
		args = []any{stored.Version, stored.ScenarioName, string(stored.Status), stored.CurrentNodeID, stored.ParentRunID, threadID, stored.ForkFromVersion, data, stored.RunID, expectedVersion, int64(fenceToken)}
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("postgres runstate: update snapshot %q: %w", stored.RunID, err)
	}
	return requireAffected(result, 1)
}

// maxEventSequenceTx returns the highest per-run sequence already taken,
// considering both delivered events and still-unpublished outbox rows. The
// caller must hold the per-run advisory lock inside tx.
func (r *Repository) maxEventSequenceTx(ctx context.Context, tx *sql.Tx, runID string) (int64, error) {
	query := fmt.Sprintf(`SELECT GREATEST(
	COALESCE((SELECT MAX(sequence) FROM %s WHERE run_id = $1), 0),
	COALESCE((SELECT MAX(sequence) FROM %s WHERE run_id = $1), 0)
)`, r.eventTable, r.outboxTable)
	var maxSeq int64
	if err := tx.QueryRowContext(ctx, query, runID).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("postgres runstate: max event sequence for run %q: %w", runID, err)
	}
	return maxSeq, nil
}

func loadSnapshotTx(ctx context.Context, tx *sql.Tx, table, runID string) (runstate.RunSnapshot, error) {
	query := fmt.Sprintf(`SELECT snapshot_json FROM %s WHERE run_id = $1`, table)
	var data []byte
	if err := tx.QueryRowContext(ctx, query, runID).Scan(&data); err != nil {
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

// marshalOutboxEvent serializes the full event envelope for the outbox's
// payload_json column, so the relay can redeliver occurred_at, correlation,
// and trace fields exactly as emitted.
func marshalOutboxEvent(event core.Event) ([]byte, error) {
	event = obspkg.NormalizeEvent(event, time.Now().UTC())
	return json.Marshal(event)
}

// FetchUnpublishedOutbox implements runstate.OutboxRepository.
func (r *Repository) FetchUnpublishedOutbox(ctx context.Context, limit int) ([]runstate.OutboxEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	query := fmt.Sprintf(`SELECT id, run_id, sequence, payload_json, created_at
FROM %s
WHERE published_at IS NULL
ORDER BY id ASC
LIMIT $1`, r.outboxTable)
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres runstate: fetch unpublished outbox: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]runstate.OutboxEvent, 0)
	for rows.Next() {
		var row runstate.OutboxEvent
		var runID string
		var payload []byte
		if err := rows.Scan(&row.ID, &runID, &row.Sequence, &payload, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres runstate: scan outbox row: %w", err)
		}
		if err := json.Unmarshal(payload, &row.Event); err != nil {
			return nil, fmt.Errorf("postgres runstate: decode outbox event %d: %w", row.ID, err)
		}
		row.Event.RunID = runID
		row.CreatedAt = row.CreatedAt.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres runstate: fetch unpublished outbox rows: %w", err)
	}
	return out, nil
}

// MarkOutboxPublished implements runstate.OutboxRepository. The conditional
// update makes concurrent relays on several nodes safe: only the first
// marker affects a row, and an already-published row is not an error.
func (r *Repository) MarkOutboxPublished(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	query := fmt.Sprintf(`UPDATE %s SET published_at = NOW() WHERE id = $1 AND published_at IS NULL`, r.outboxTable)
	if _, err := r.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("postgres runstate: mark outbox %d published: %w", id, err)
	}
	return nil
}

// DeleteOutboxForRun implements runstate.OutboxRepository.
func (r *Repository) DeleteOutboxForRun(ctx context.Context, runID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE run_id = $1`, r.outboxTable)
	result, err := r.db.ExecContext(ctx, query, runID)
	if err != nil {
		return 0, fmt.Errorf("postgres runstate: delete outbox for run %q: %w", runID, err)
	}
	return result.RowsAffected()
}

// PurgeOutboxPublishedBefore implements runstate.OutboxRepository. Only
// published rows in the caller's tenant scope are removed; unpublished rows
// are undelivered events.
func (r *Repository) PurgeOutboxPublishedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	scope, err := runstate.ScopeListFilter(ctx, runstate.ListFilter{})
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`DELETE FROM %s
WHERE published_at IS NOT NULL
  AND published_at < $1
  AND ($2 = '' OR payload_json->>'tenant_id' = $2)`, r.outboxTable)
	result, err := r.db.ExecContext(ctx, query, cutoff.UTC(), scope.TenantID)
	if err != nil {
		return 0, fmt.Errorf("postgres runstate: purge published outbox: %w", err)
	}
	return result.RowsAffected()
}
