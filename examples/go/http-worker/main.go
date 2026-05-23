package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	agentflow "github.com/aijustin/agentflow-go"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenario := examplescenario.AutonomousEcho()
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
	if err != nil {
		log.Fatal(err)
	}

	recorder := agentflow.NewPrometheusRecorder()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var tracer observability.Tracer
	if os.Getenv("AGENTFLOW_OTEL_STDOUT") == "1" {
		provider, err := agentflow.NewOpenTelemetryStdoutTracerProvider(ctx, agentflow.OpenTelemetryTracerProviderConfig{
			ServiceName:    "agentflow-http-worker",
			ServiceVersion: agentflow.Version,
		})
		if err != nil {
			log.Fatal(err)
		}
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = provider.Shutdown(shutdownCtx)
		}()
		tracer = agentflow.OpenTelemetryTracerFromProvider(provider, "github.com/aijustin/agentflow-go/examples/http-worker")
		fmt.Println("OpenTelemetry stdout tracing enabled (AGENTFLOW_OTEL_STDOUT=1)")
	}

	eventStore := agentflow.NewInMemoryEventStore()
	eventHub := agentflow.NewEventHub()
	eventSink := agentflow.NewObservabilityEventSink(
		recorder,
		tracer,
		agentflow.NewEventFanoutSink(
			agentflow.NewEventStoreSink(eventStore, eventHub),
			agentflow.NewSlogEventSink(logger),
		),
	)

	queue := agentflow.NewInMemoryJobQueue()
	opts = append(opts,
		agentflow.WithJobQueue(queue),
		agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()),
		agentflow.WithHITLTokenSecret([]byte("dev-secret"), os.Stderr),
		agentflow.WithRecorder(recorder),
		agentflow.WithEventSink(eventSink),
	)
	if tracer != nil {
		opts = append(opts, agentflow.WithTracer(tracer))
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		log.Fatal(err)
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	studioSavePath := envOr("AGENT_STUDIO_SCENARIO_PATH", filepath.Join(".studio", "scenario.yaml"))
	if err := os.MkdirAll(filepath.Dir(studioSavePath), 0o700); err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(studioSavePath); os.IsNotExist(err) {
		if err := configyaml.SaveFile(studioSavePath, scenario); err != nil {
			log.Fatal(err)
		}
	}

	handler, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
		Queue:          queue,
		Policy:         security.NewDefaultRolePolicy(),
		Framework:      fw,
		Version:        agentflow.Version,
		MetricsHandler: agentflow.PrometheusMetricsHandler(recorder),
		StudioSavePath: studioSavePath,
	})
	if err != nil {
		log.Fatal(err)
	}

	dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
		Store:          eventStore,
		Hub:            eventHub,
		Framework:      fw,
		StudioSavePath: studioSavePath,
	})
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.Handle("/observability/", http.StripPrefix("/observability", dashboard))

	jobHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		log.Fatal(err)
	}
	worker, err := async.NewWorker(queue, jobHandler, async.WorkerConfig{
		WorkerID:    "example-worker",
		Concurrency: 2,
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := envOr("AGENT_HTTP_ADDR", "127.0.0.1:7060")

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

	if interval := envDuration("AGENT_RETENTION_INTERVAL", 0); interval > 0 {
		maxAge := envDuration("AGENT_RETENTION_MAX_AGE", 7*24*time.Hour)
		go runRetentionLoop(ctx, fw, interval, maxAge)
		fmt.Printf("retention worker enabled (interval=%s max_age=%s; POST /v1/admin/retention/*)\n", interval, maxAge)
	}

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		fmt.Printf("HTTP server listening on %s (metrics at /metrics, studio at /observability/, save path %s)\n", addr, studioSavePath)
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

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return fallback
}

func runRetentionLoop(ctx context.Context, fw *agentflow.Framework, interval, maxAge time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	runOnce := func() {
		removed, err := fw.PurgeWithPolicy(ctx, agentflow.RetentionPolicy{MaxAge: maxAge})
		if err != nil {
			log.Printf("retention purge: %v", err)
			return
		}
		gc, err := fw.PurgeOrphanBlobs(ctx)
		if err != nil {
			log.Printf("retention blob gc: %v", err)
			return
		}
		if removed > 0 || gc > 0 {
			log.Printf("retention: removed %d runs, %d orphan blobs", removed, gc)
		}
	}
	runOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
