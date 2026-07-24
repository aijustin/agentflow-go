// Command apply-migrations applies the embedded agentflow PostgreSQL schema
// migrations to the database named by AGENT_POSTGRES_DSN (or the first
// argument). Already-applied versions are skipped, concurrent runs are
// serialized with a pg advisory lock, and each applied version is recorded in
// agentflow_schema_migrations.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/aijustin/agentflow-go/migrations/postgres"
)

const defaultDSN = "postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable"

func main() {
	dsn := os.Getenv("AGENT_POSTGRES_DSN")
	if len(os.Args) > 1 {
		dsn = os.Args[1]
	}
	if dsn == "" {
		dsn = defaultDSN
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fatal(fmt.Errorf("connect: %w", err))
	}
	if err := migrations.Migrate(ctx, db); err != nil {
		fatal(err)
	}
	version, err := migrations.AppliedVersion(ctx, db)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("migrations applied; schema version %d\n", version)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "apply-migrations: %v\n", err)
	os.Exit(1)
}
