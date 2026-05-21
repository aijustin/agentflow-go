package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

func main() {
	scenarioFile := "../../autonomous.yaml"
	cfg := agentflow.ProductionConfig{
		ScenarioFile: scenarioFile,
		TokenSecret:  "dev-secret",
		HTTPAddr:     "127.0.0.1:8080",
		QueueKind:    "memory",
		Version:      agentflow.Version,
		WorkerID:     "example-worker",
		Concurrency:  2,
	}

	fw, err := agentflow.NewProduction(cfg, os.Stderr)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	queue, err := agentflow.NewProductionQueue(cfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := agentflow.BuildProductionHTTPHandler(cfg, fw, queue)
	if err != nil {
		log.Fatal(err)
	}

	jobHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		log.Fatal(err)
	}
	worker, err := asyncpkg.NewWorker(queue, jobHandler, asyncpkg.WorkerConfig{
		WorkerID:   cfg.WorkerID,
		Concurrency: cfg.Concurrency,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker stopped: %v", err)
		}
	}()

	server := &http.Server{Addr: cfg.HTTPAddr, Handler: handler}
	go func() {
		fmt.Printf("HTTP server listening on %s\n", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server stopped: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}
