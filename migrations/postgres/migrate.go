// Package postgres applies and inspects the versioned PostgreSQL schema
// migrations embedded from this directory (the NNNN_*.up.sql files).
//
// Migrations are tracked in the agentflow_schema_migrations table (created by
// the runner up front and formally owned by migration 0004), so reruns skip
// versions that are already applied. Concurrent first boots are serialized
// with a PostgreSQL advisory lock held on a single connection for the whole
// run, and each migration plus its version record commits in one transaction.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed *.up.sql
var migrationFS embed.FS

// RequiredVersion is the minimum applied schema version this codebase needs.
// Version 4 adds agentflow_run_snapshots.fence_token, which fenced snapshot
// saves (runstate.FencedRepository) depend on; version 5 adds the
// agentflow_outbox table used by transactional event outbox writes and the
// framework outbox relay; version 6 adds tenant isolation to runtime events;
// version 7 adds tenant isolation to async jobs.
// ValidateWiring refuses to start against an older schema so missing
// columns/tables surface at boot instead of at runtime.
const RequiredVersion = 7

// schemaMigrationsTable is quoted into DDL/DML below; it is a fixed
// identifier, never user input.
const schemaMigrationsTable = "agentflow_schema_migrations"

// migrateAdvisoryLockKey serializes concurrent migration runs across
// instances (pg advisory lock; any fixed int64 works).
const migrateAdvisoryLockKey int64 = 72631549

type migration struct {
	version int
	name    string
	body    string
}

// Migrate applies every embedded *.up.sql migration whose version is not yet
// recorded in agentflow_schema_migrations, in version order. It is safe to
// call on every boot and from several instances at once.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("postgres migrations: db is nil")
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	// Hold a session-level advisory lock on ONE connection so two instances
	// starting against the same database cannot interleave DDL (a pool would
	// run the lock and the DDL on different sessions). Unlocked on return
	// (defer) and implicitly on connection close.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("postgres migrations: connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateAdvisoryLockKey); err != nil {
		return fmt.Errorf("postgres migrations: acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateAdvisoryLockKey)
	}()

	if err := ensureVersionTable(ctx, conn); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("postgres migrations: begin %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, m.body); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("postgres migrations: apply %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+schemaMigrationsTable+` (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`, m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("postgres migrations: record %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("postgres migrations: commit %s: %w", m.name, err)
		}
	}
	return nil
}

// AppliedVersion returns the highest recorded migration version. A database
// without the version table (never migrated) reports version 0 so callers can
// treat "no bookkeeping" as "nothing applied" instead of an error.
func AppliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("postgres migrations: db is nil")
	}
	var version int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM `+schemaMigrationsTable).Scan(&version)
	if err != nil {
		if isUndefinedTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("postgres migrations: read schema version: %w", err)
	}
	return version, nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return nil, fmt.Errorf("postgres migrations: read embedded files: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("postgres migrations: read %s: %w", name, err)
		}
		migrations = append(migrations, migration{version: version, name: name, body: string(body)})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("postgres migrations: duplicate version %d (%s and %s)",
				migrations[i].version, migrations[i-1].name, migrations[i].name)
		}
	}
	return migrations, nil
}

// migrationVersion parses the leading NNNN_ prefix of a migration file name.
func migrationVersion(name string) (int, error) {
	prefix, _, _ := strings.Cut(name, "_")
	version, err := strconv.Atoi(prefix)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("postgres migrations: malformed migration file name %q", name)
	}
	return version, nil
}

// ensureVersionTable creates the bookkeeping table before any migration runs
// so versions can be recorded from 0001 on. Migration 0004 owns the same
// table with identical DDL (IF NOT EXISTS), so reaching it is a no-op.
func ensureVersionTable(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+schemaMigrationsTable+` (
  version integer PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
)`)
	if err != nil {
		return fmt.Errorf("postgres migrations: ensure version table: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *sql.Conn) (map[int]bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version FROM `+schemaMigrationsTable)
	if err != nil {
		return nil, fmt.Errorf("postgres migrations: list applied versions: %w", err)
	}
	defer rows.Close()
	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("postgres migrations: scan applied version: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// isUndefinedTable reports whether err is PostgreSQL's undefined_table
// (SQLSTATE 42P01), via the pgx error type when available and with a string
// fallback for other drivers.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return strings.Contains(err.Error(), "does not exist") &&
		strings.Contains(err.Error(), schemaMigrationsTable)
}
