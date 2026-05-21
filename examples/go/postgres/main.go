package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	agentflow "github.com/aijustin/agentflow-go"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	scenarioFile := "../../autonomous.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := agentflow.DemoWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}

	opts, err := agentflow.DevelopmentOptions(scenario, agentflow.DevelopmentConfig{WorkDir: workDir})
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
		fileOpts, err := agentflow.ProductionOptions(agentflow.ProductionConfig{StateDir: stateDir}, scenario, workDir)
		if err != nil {
			log.Fatal(err)
		}
		opts = append(opts, fileOpts...)
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
