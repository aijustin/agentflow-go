package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigrationVersionParsing(t *testing.T) {
	cases := map[string]int{
		"0001_agentflow_core.up.sql":    1,
		"0004_agentflow_fencing.up.sql": 4,
		"0012_something.up.sql":         12,
		"7_no_padding.up.sql":           7,
	}
	for name, want := range cases {
		got, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("migrationVersion(%q): %v", name, err)
		}
		if got != want {
			t.Fatalf("migrationVersion(%q) = %d, want %d", name, got, want)
		}
	}
	for _, bad := range []string{"agentflow.up.sql", "0000_x.up.sql", "_x.up.sql", "abcd_x.up.sql"} {
		if _, err := migrationVersion(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestLoadMigrationsSortedAndComplete(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 4 {
		t.Fatalf("expected at least 4 embedded migrations, got %d", len(migrations))
	}
	seen := map[int]string{}
	max := 0
	for i, m := range migrations {
		if i > 0 && m.version <= migrations[i-1].version {
			t.Fatalf("migrations not sorted or duplicate version: %+v", migrations)
		}
		seen[m.version] = m.name
		if m.version > max {
			max = m.version
		}
	}
	// Versions must be contiguous from 1: a gap would leave AppliedVersion
	// reporting a schema the code cannot reason about.
	for v := 1; v <= max; v++ {
		if _, ok := seen[v]; !ok {
			t.Fatalf("missing migration version %d (have %v)", v, seen)
		}
	}
	if max < RequiredVersion {
		t.Fatalf("RequiredVersion %d has no embedded migration (max %d)", RequiredVersion, max)
	}
}

func TestFencingMigrationContents(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var fencing *migration
	for i := range migrations {
		if migrations[i].version == 4 {
			fencing = &migrations[i]
		}
	}
	if fencing == nil {
		t.Fatal("migration 0004 not embedded")
	}
	for _, want := range []string{"fence_token", "agentflow_schema_migrations", "agentflow_run_snapshots"} {
		if !strings.Contains(fencing.body, want) {
			t.Fatalf("migration 0004 must mention %q", want)
		}
	}
}

func TestOutboxMigrationContents(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var outbox *migration
	for i := range migrations {
		if migrations[i].version == 5 {
			outbox = &migrations[i]
		}
	}
	if outbox == nil {
		t.Fatal("migration 0005 not embedded")
	}
	for _, want := range []string{"agentflow_outbox", "published_at", "payload_json", "agentflow_outbox_unpublished_idx"} {
		if !strings.Contains(outbox.body, want) {
			t.Fatalf("migration 0005 must mention %q", want)
		}
	}
}

func TestJobTenantMigrationContents(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var jobs *migration
	for i := range migrations {
		if migrations[i].version == 7 {
			jobs = &migrations[i]
		}
	}
	if jobs == nil {
		t.Fatal("migration 0007 not embedded")
	}
	for _, want := range []string{"agentflow_jobs", "tenant_id", "agentflow_jobs_tenant_state_idx"} {
		if !strings.Contains(jobs.body, want) {
			t.Fatalf("migration 0007 must mention %q", want)
		}
	}
}

func TestIsUndefinedTable(t *testing.T) {
	if !isUndefinedTable(&pgconn.PgError{Code: "42P01"}) {
		t.Fatal("pgx 42P01 must be recognized")
	}
	if isUndefinedTable(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique violation is not undefined_table")
	}
	wrapped := fmt.Errorf("query failed: %w", &pgconn.PgError{Code: "42P01"})
	if !isUndefinedTable(wrapped) {
		t.Fatal("wrapped 42P01 must be recognized")
	}
	// Non-pgx drivers fall back to message matching.
	if !isUndefinedTable(errors.New(`pq: relation "agentflow_schema_migrations" does not exist`)) {
		t.Fatal("pq-style message must be recognized")
	}
	if isUndefinedTable(errors.New("connection refused")) {
		t.Fatal("unrelated error must not be recognized")
	}
}
