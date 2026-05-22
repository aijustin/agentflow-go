package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const DefaultCheckpointHistoryTable = "agentflow_run_checkpoint_history"

type CheckpointHistoryOption func(*CheckpointHistory) error

type CheckpointHistory struct {
	db    *sql.DB
	table string
}

func NewCheckpointHistory(db *sql.DB, opts ...CheckpointHistoryOption) (*CheckpointHistory, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres checkpoint history: db is nil")
	}
	h := &CheckpointHistory{db: db, table: DefaultCheckpointHistoryTable}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(h); err != nil {
			return nil, err
		}
	}
	return h, nil
}

func WithCheckpointHistoryTable(name string) CheckpointHistoryOption {
	return func(h *CheckpointHistory) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres checkpoint history: invalid table name %q", name)
		}
		h.table = name
		return nil
	}
}

func (h *CheckpointHistory) Append(ctx context.Context, snapshot runstate.RunSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres checkpoint history: marshal snapshot: %w", err)
	}
	query := fmt.Sprintf(`INSERT INTO %s (run_id, version, status, current_node_id, step_count, snapshot_json, recorded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (run_id, version) DO NOTHING`, h.table)
	_, err = h.db.ExecContext(ctx, query,
		snapshot.RunID,
		snapshot.Version,
		string(snapshot.Status),
		snapshot.CurrentNodeID,
		len(snapshot.StepOutputs),
		data,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("postgres checkpoint history: append: %w", err)
	}
	return nil
}

func (h *CheckpointHistory) List(ctx context.Context, runID string, limit int) ([]runstate.CheckpointSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limitSQL := ""
	args := []any{runID}
	if limit > 0 {
		args = append(args, limit)
		limitSQL = fmt.Sprintf(" LIMIT $%d", len(args))
	}
	query := fmt.Sprintf(`SELECT run_id, version, status, current_node_id, step_count, recorded_at
FROM %s
WHERE run_id = $1
ORDER BY version ASC%s`, h.table, limitSQL)
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres checkpoint history: list: %w", err)
	}
	defer rows.Close()
	out := make([]runstate.CheckpointSummary, 0)
	for rows.Next() {
		var summary runstate.CheckpointSummary
		if err := rows.Scan(&summary.RunID, &summary.Version, &summary.Status, &summary.CurrentNodeID, &summary.StepCount, &summary.RecordedAt); err != nil {
			return nil, fmt.Errorf("postgres checkpoint history: scan: %w", err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (h *CheckpointHistory) Load(ctx context.Context, runID string, version int64) (runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	query := fmt.Sprintf(`SELECT snapshot_json FROM %s WHERE run_id = $1 AND version = $2`, h.table)
	var data []byte
	if err := h.db.QueryRowContext(ctx, query, runID, version).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runstate.RunSnapshot{}, runstate.ErrNotFound
		}
		return runstate.RunSnapshot{}, fmt.Errorf("postgres checkpoint history: load: %w", err)
	}
	var snapshot runstate.RunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("postgres checkpoint history: decode: %w", err)
	}
	return snapshot, nil
}
