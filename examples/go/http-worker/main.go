package main

import (
	"context"
	"encoding/json"
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
	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
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
	// Demo LLM: a fallback mock whose queued tool-call turns play the AI graph
	// composer. Keep several identical catalog sessions because autonomous
	// trial runs share this local mock and may consume one queued session
	// while exercising the Studio before AI composition. A real provider
	// needs no such setup.
	demoGateway := llmmock.NewFallbackGateway()
	demoGateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	for range 8 {
		queueComposerTurn(demoGateway, "p1", "compose_list_parts", `{}`)
		queueComposerTurn(demoGateway, "p2", "compose_add_node", `{"id":"echo_input","kind":"tool","ref":"echo"}`)
		queueComposerTurn(demoGateway, "p3", "compose_add_node", `{"id":"mark_done","kind":"transform","input":{"set":{"done":true}}}`)
		queueComposerTurn(demoGateway, "p4", "compose_connect", `{"from":"echo_input","to":"mark_done"}`)
		queueComposerTurn(demoGateway, "p5", "compose_finish", `{}`)
		demoGateway.QueueToolCall("default", llm.ToolCallResponse{
			ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "graph ready"}},
		})
	}
	opts = append(opts, agentflow.WithLLMGateway(demoGateway))

	recorder := adapters.NewPrometheusRecorder()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var tracer observability.Tracer
	if os.Getenv("AGENTFLOW_OTEL_STDOUT") == "1" {
		provider, err := adapters.NewOpenTelemetryStdoutTracerProvider(ctx, adapters.OpenTelemetryTracerProviderConfig{
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
		tracer = adapters.OpenTelemetryTracerFromProvider(provider, "github.com/aijustin/agentflow-go/examples/http-worker")
		fmt.Println("OpenTelemetry stdout tracing enabled (AGENTFLOW_OTEL_STDOUT=1)")
	}

	eventStore := adapters.NewInMemoryEventStore()
	eventHub := adapters.NewEventHub()
	eventSink := adapters.NewObservabilityEventSink(
		recorder,
		tracer,
		adapters.NewEventFanoutSink(
			adapters.NewEventStoreSink(eventStore, eventHub),
			adapters.NewSlogEventSink(logger),
		),
	)

	queue := adapters.NewInMemoryJobQueue()
	opts = append(opts,
		agentflow.WithJobQueue(queue),
		agentflow.WithCheckpointHistory(adapters.NewInMemoryCheckpointHistory()),
		agentflow.WithHITLTokenSecret([]byte("dev-secret-16bytes"), os.Stderr),
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

	handler, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue:          queue,
		Policy:         security.NewDefaultRolePolicy(),
		Framework:      fw,
		Version:        agentflow.Version,
		MetricsHandler: adapters.PrometheusMetricsHandler(recorder),
		StudioSavePath: studioSavePath,
		// This example binds to loopback by default. Production deployments
		// must configure AuthMiddleware instead of enabling this opt-out.
		InsecureAllowNoAuth: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	dashboard, err := httpx.NewObservabilityHTTPHandler(httpx.ObservabilityHTTPHandlerConfig{
		Store:          eventStore,
		Hub:            eventHub,
		Framework:      fw,
		StudioSavePath: studioSavePath,
		// Local demo: no auth middleware, so explicitly open the dashboard
		// and API routes for the Studio UI.
		InsecureAllowNoAuth: true,
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

func queueComposerTurn(gateway *llmmock.FallbackGateway, id, tool, input string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: tool, Input: json.RawMessage(input)}},
	})
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
