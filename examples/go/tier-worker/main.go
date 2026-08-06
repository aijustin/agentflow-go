// tier-worker runs the tier-memory builder stack with Postgres warm tier, file or blob cold tier, and
// async memory.reconcile jobs via a shared job queue.
//
// Prerequisites (reference stack):
//
//	cd examples/deploy && docker compose up -d
//	export AGENT_POSTGRES_DSN='postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable'
//	export AGENTFLOW_HITL_SECRET='replace-with-at-least-16-bytes'
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
	"strings"
	"syscall"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	scenario := examplescenario.TierMemory()
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
	if err != nil {
		log.Fatal(err)
	}

	recorder := adapters.NewPrometheusRecorder()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	hitlSecret := []byte(os.Getenv("AGENTFLOW_HITL_SECRET"))
	if len(hitlSecret) < runstate.MinTokenSecretLength {
		log.Fatalf("AGENTFLOW_HITL_SECRET must contain at least %d bytes", runstate.MinTokenSecretLength)
	}

	var queue async.Queue
	var tierStoreMetrics tier.Store
	if dsn := os.Getenv("AGENT_POSTGRES_DSN"); dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if err := db.PingContext(ctx); err != nil {
			log.Fatal(err)
		}
		repo, err := adapters.NewPostgresRunStateRepository(db)
		if err != nil {
			log.Fatal(err)
		}
		queue, err = adapters.NewPostgresJobQueue(db)
		if err != nil {
			log.Fatal(err)
		}
		tierStore, coldDir, err := newCompositeTierStore(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		tierStoreMetrics = tierStore
		policy := tierPolicyFromScenario(scenario)
		opts = append(opts,
			agentflow.WithRunStateRepository(repo),
			agentflow.WithDatabase(db),
			agentflow.WithTierStore("session", tierStore, policy),
		)
		fmt.Printf("using postgres run-state, job queue, and composite tier store (cold=%s)\n", coldDir)
	} else {
		queue = adapters.NewInMemoryJobQueue()
		fmt.Println("AGENT_POSTGRES_DSN not set; using in-memory queue and default in-memory tier store")
	}

	opts = append(opts,
		agentflow.WithJobQueue(queue),
		agentflow.WithHITLTokenSecret(hitlSecret, os.Stderr),
		agentflow.WithRecorder(recorder),
		agentflow.WithEventSink(adapters.NewObservabilityEventSink(recorder, nil, adapters.NewSlogEventSink(logger))),
	)
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		log.Fatal(err)
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = fw.Close(context.Background()) }()

	handler, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue:          queue,
		Policy:         security.NewDefaultRolePolicy(),
		Framework:      fw,
		Version:        agentflow.Version,
		MetricsHandler: adapters.PrometheusMetricsHandler(recorder),
		// This example binds to loopback by default. Production deployments
		// must configure AuthMiddleware instead of enabling this opt-out.
		InsecureAllowNoAuth: true,
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

	addr := envOr("AGENT_HTTP_ADDR", "127.0.0.1:7070")
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
				if tierStoreMetrics != nil {
					tier.RecordTierDepth(ctx, tierStoreMetrics, recorder, scenario.Name, memory.Namespace{
						Scope: memory.ScopeSession, SessionID: "tier-worker", Agent: "assistant",
					})
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
	warm, err := adapters.NewPostgresTierWarmStore(adapters.PostgresTierWarmStoreConfig{DB: db})
	if err != nil {
		return nil, "", err
	}
	cold, coldLabel, err := newColdTierStore()
	if err != nil {
		return nil, "", err
	}
	store := adapters.NewCompositeTierStore(adapters.CompositeTierStoreConfig{
		Hot:  adapters.NewInMemoryTierHotStore(),
		Warm: warm,
		Cold: cold,
	})
	return store, coldLabel, nil
}

func newColdTierStore() (tier.Store, string, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_TIER_COLD_BACKEND"))) {
	case "blob":
		indexDir := os.Getenv("AGENT_TIER_COLD_INDEX_DIR")
		if indexDir == "" {
			indexDir = filepath.Join(os.TempDir(), "agentflow-tier-cold-index")
		}
		if err := os.MkdirAll(indexDir, 0o700); err != nil {
			return nil, "", err
		}
		blobs, blobLabel, err := openBlobAdmin()
		if err != nil {
			return nil, "", err
		}
		cold, err := adapters.NewBlobTierColdStore(adapters.BlobTierColdStoreConfig{
			Blobs:    blobs,
			IndexDir: indexDir,
		})
		if err != nil {
			return nil, "", err
		}
		return cold, fmt.Sprintf("blob(%s,index=%s)", blobLabel, indexDir), nil
	default:
		coldDir := os.Getenv("AGENT_TIER_COLD_DIR")
		if coldDir == "" {
			coldDir = filepath.Join(os.TempDir(), "agentflow-tier-cold")
		}
		if err := os.MkdirAll(coldDir, 0o700); err != nil {
			return nil, "", err
		}
		cold, err := adapters.NewFileTierColdStore(coldDir)
		if err != nil {
			return nil, "", err
		}
		return cold, coldDir, nil
	}
}

func openBlobAdmin() (runstate.BlobAdmin, string, error) {
	if endpoint := os.Getenv("AGENT_S3_ENDPOINT"); endpoint != "" {
		store, err := adapters.NewS3BlobStore(adapters.S3BlobStoreConfig{
			Endpoint:        endpoint,
			Bucket:          envOr("AGENT_S3_BUCKET", "agentflow"),
			Region:          envOr("AGENT_S3_REGION", "us-east-1"),
			Prefix:          os.Getenv("AGENT_S3_PREFIX"),
			AccessKeyID:     os.Getenv("AGENT_S3_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("AGENT_S3_SECRET_KEY"),
		})
		if err != nil {
			return nil, "", err
		}
		admin, ok := store.(runstate.BlobAdmin)
		if !ok {
			return nil, "", fmt.Errorf("s3 blob store does not implement BlobAdmin")
		}
		return admin, envOr("AGENT_S3_BUCKET", "agentflow"), nil
	}
	dir := os.Getenv("AGENT_BLOB_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "agentflow-blobs")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	store, err := adapters.NewFileBlobStore(dir)
	if err != nil {
		return nil, "", err
	}
	admin, ok := store.(runstate.BlobAdmin)
	if !ok {
		return nil, "", fmt.Errorf("file blob store does not implement BlobAdmin")
	}
	return admin, dir, nil
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
