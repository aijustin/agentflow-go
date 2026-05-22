package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	scenario := examplescenario.AutonomousEcho()

	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
	if err != nil {
		log.Fatal(err)
	}

	if dsn := os.Getenv("AGENT_POSTGRES_DSN"); dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		if err := db.Ping(); err != nil {
			log.Fatal(err)
		}
		repo, err := agentflow.NewPostgresRunStateRepository(db)
		if err != nil {
			log.Fatal(err)
		}
		opts = append(opts, agentflow.WithRunStateRepository(repo), agentflow.WithDatabase(db))
		fmt.Println("using postgres run-state repository")
	} else {
		stateDir, err := os.MkdirTemp("", "agentflow-postgres-example-*")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(stateDir)
		repo, err := agentflow.NewFileRunStateRepository(filepath.Join(stateDir, "runs"))
		if err != nil {
			log.Fatal(err)
		}
		blobs, err := agentflow.NewFileBlobStore(filepath.Join(stateDir, "blobs"))
		if err != nil {
			log.Fatal(err)
		}
		opts = append(opts, agentflow.WithRunStateRepository(repo), agentflow.WithBlobStore(blobs))
		fmt.Println("AGENT_POSTGRES_DSN not set; using file-backed run-state in", stateDir)
	}

	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:  "postgres-example-run",
		Agent:  "assistant",
		Prompt: "persist this run",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Output)
}
