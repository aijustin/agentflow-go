package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

const DefaultTableName = "agentflow_memory_tier_records"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Option func(*Store) error

type Store struct {
	db    *sql.DB
	table string
	level tier.Level
}

func NewStore(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres tier store: db is nil")
	}
	store := &Store{db: db, table: DefaultTableName, level: tier.LevelWarm}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(store); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func WithTableName(name string) Option {
	return func(store *Store) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres tier store: invalid table name %q", name)
		}
		store.table = name
		return nil
	}
}

func WithLevel(level tier.Level) Option {
	return func(store *Store) error {
		if level == "" {
			return fmt.Errorf("postgres tier store: level is required")
		}
		store.level = level
		return nil
	}
}

func (s *Store) Put(ctx context.Context, ns memory.Namespace, record tier.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.ID == "" {
		return fmt.Errorf("postgres tier store: record id is required")
	}
	record.Tier = s.level
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("postgres tier store: marshal record %q: %w", record.ID, err)
	}
	lastAccess := record.LastAccessAt
	if lastAccess.IsZero() {
		lastAccess = time.Now().UTC()
	}
	query := fmt.Sprintf(`INSERT INTO %s (namespace_key, record_id, tier, last_access_at, record_json)
VALUES ($1, $2, $3, $4, $5::jsonb)
ON CONFLICT (namespace_key, record_id) DO UPDATE SET
tier = EXCLUDED.tier,
last_access_at = EXCLUDED.last_access_at,
record_json = EXCLUDED.record_json`, s.table)
	if _, err := s.db.ExecContext(ctx, query, ns.KeyPrefix(), record.ID, string(s.level), lastAccess, raw); err != nil {
		return fmt.Errorf("postgres tier store: put %q: %w", record.ID, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, ns memory.Namespace, id string) (tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return tier.Record{}, err
	}
	query := fmt.Sprintf(`SELECT record_json FROM %s WHERE namespace_key = $1 AND record_id = $2 AND tier = $3`, s.table)
	var raw []byte
	if err := s.db.QueryRowContext(ctx, query, ns.KeyPrefix(), id, string(s.level)).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return tier.Record{}, memory.ErrNotFound
		}
		return tier.Record{}, fmt.Errorf("postgres tier store: get %q: %w", id, err)
	}
	var record tier.Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return tier.Record{}, fmt.Errorf("postgres tier store: decode record %q: %w", id, err)
	}
	return record, nil
}

func (s *Store) List(ctx context.Context, ns memory.Namespace, level tier.Level, limit int) ([]tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if level != s.level {
		return nil, nil
	}
	query := fmt.Sprintf(`SELECT record_json FROM %s
WHERE namespace_key = $1 AND tier = $2
ORDER BY last_access_at DESC`, s.table)
	args := []any{ns.KeyPrefix(), string(s.level)}
	if limit > 0 {
		query += " LIMIT $3"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres tier store: list: %w", err)
	}
	defer rows.Close()
	out := make([]tier.Record, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres tier store: scan list row: %w", err)
		}
		var record tier.Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("postgres tier store: decode list row: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres tier store: list rows: %w", err)
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ns memory.Namespace, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE namespace_key = $1 AND record_id = $2 AND tier = $3`, s.table)
	result, err := s.db.ExecContext(ctx, query, ns.KeyPrefix(), id, string(s.level))
	if err != nil {
		return fmt.Errorf("postgres tier store: delete %q: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres tier store: delete rows affected %q: %w", id, err)
	}
	if rows == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *Store) Count(ctx context.Context, ns memory.Namespace, level tier.Level) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if level != s.level {
		return 0, nil
	}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE namespace_key = $1 AND tier = $2`, s.table)
	var count int
	if err := s.db.QueryRowContext(ctx, query, ns.KeyPrefix(), string(s.level)).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres tier store: count: %w", err)
	}
	return count, nil
}

func validTableName(name string) bool {
	return identifierPattern.MatchString(name)
}
