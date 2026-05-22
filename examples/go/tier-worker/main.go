// tier-worker runs tier_memory.yaml with Postgres warm tier, file cold tier, and
// async memory.reconcile jobs via a shared job queue.
//
// Prerequisites (reference stack):
//
//	cd examples/deploy && docker compose up -d
//	export AGENT_POSTGRES_DSN='postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable'
//	./init/apply-migrations.sh
//	go run ./examples/go/tier-worker/main.go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	scenarioFile := "../../tier_memory.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
	if err != nil {
		log.Fatal(err)
	}

	recorder := agentflow.NewPrometheusRecorder()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var queue async.Queue
	if dsn := os.Getenv("AGENT_POSTGRES_DSN"); dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		if err := db.PingContext(ctx); err != nil {
			log.Fatal(err)
		}
		repo, err := agentflow.NewPostgresRunStateRepository(db)
		if err != nil {
			log.Fatal(err)
		}
		queue, err = agentflow.NewPostgresJobQueue(db)
		if err != nil {
			log.Fatal(err)
		}
		tierStore, coldDir, err := newCompositeTierStore(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		policy := tierPolicyFromScenario(scenario)
		opts = append(opts,
			agentflow.WithRunStateRepository(repo),
			agentflow.WithDatabase(db),
			agentflow.WithTierStore("session", tierStore, policy),
		)
		fmt.Printf("using postgres run-state, job queue, and composite tier store (cold=%s)\n", coldDir)
	} else {
		queue = agentflow.NewInMemoryJobQueue()
		fmt.Println("AGENT_POSTGRES_DSN not set; using in-memory queue and default in-memory tier store")
	}

	opts = append(opts,
		agentflow.WithJobQueue(queue),
		agentflow.WithHITLTokenSecret([]byte("dev-secret"), os.Stderr),
		agentflow.WithRecorder(recorder),
		agentflow.WithEventSink(agentflow.NewObservabilityEventSink(recorder, nil, agentflow.NewSlogEventSink(logger))),
	)
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		log.Fatal(err)
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	handler, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
		Queue:          queue,
		Policy:         security.NewDefaultRolePolicy(),
		Framework:      fw,
		Version:        agentflow.Version,
		MetricsHandler: agentflow.PrometheusMetricsHandler(recorder),
	})
	if err != nil {
		log.Fatal(err)
	}
	jobHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		log.Fatal(err)
	}
	worker, err := async.NewWorker(queue, jobHandler, async.WorkerConfig{
		WorkerID:    "tier-worker",
		Concurrency: 2,
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := envOr("AGENT_HTTP_ADDR", "127.0.0.1:8080")
	go func() {
		if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker stopped: %v", err)
		}
	}()

	metricsTicker := time.NewTicker(5 * time.Second)
	defer metricsTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-metricsTicker.C:
				if err := recorder.RecordQueueMetrics(ctx, queue); err != nil {
					log.Printf("queue metrics: %v", err)
				}
			}
		}
	}()

	server := &http.Server{Addr: addr, Handler: handler}
	go func() {
		fmt.Printf("tier-worker listening on %s (metrics at /metrics; memory.reconcile via shared queue)\n", addr)
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

func newCompositeTierStore(_ context.Context, db *sql.DB) (tier.Store, string, error) {
	warm, err := agentflow.NewPostgresTierWarmStore(agentflow.PostgresTierWarmStoreConfig{DB: db})
	if err != nil {
		return nil, "", err
	}
	coldDir := os.Getenv("AGENT_TIER_COLD_DIR")
	if coldDir == "" {
		coldDir = filepath.Join(os.TempDir(), "agentflow-tier-cold")
	}
	if err := os.MkdirAll(coldDir, 0o700); err != nil {
		return nil, "", err
	}
	cold, err := agentflow.NewFileTierColdStore(coldDir)
	if err != nil {
		return nil, "", err
	}
	store := agentflow.NewCompositeTierStore(agentflow.CompositeTierStoreConfig{
		Hot:  agentflow.NewInMemoryTierHotStore(),
		Warm: warm,
		Cold: cold,
	})
	return store, coldDir, nil
}

func tierPolicyFromScenario(scenario core.Scenario) tier.Policy {
	for _, ref := range scenario.Memories {
		if ref.Tiers != nil && ref.Tiers.Enabled {
			settings, ok := tier.SettingsFromCore(ref.Tiers)
			if ok {
				return settings.Policy()
			}
		}
	}
	return tier.DefaultPolicy()
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
