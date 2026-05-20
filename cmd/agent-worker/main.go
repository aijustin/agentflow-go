package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aijustin/agentflow-go/internal/cmd/agentruntime"
)

func main() {
	config, err := agentruntime.LoadConfigFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var db *sql.DB
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()
	fw, err := agentruntime.NewFramework(config, os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	queue, err := agentruntime.NewQueue(config, &db)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	worker, err := agentruntime.NewWorker(config, queue, fw)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintf(os.Stderr, "agentflow worker %s started scenario=%s queue=%s concurrency=%d\n", config.WorkerID, config.ScenarioFile, config.QueueKind, config.Concurrency)
	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
