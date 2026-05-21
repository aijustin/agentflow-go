package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenarioFile := "../../autonomous.yaml"
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

	eventSink := agentflow.NewObservabilityEventSink(
		recorder,
		tracer,
		agentflow.NewSlogEventSink(logger),
	)

	opts = append(opts,
		agentflow.WithHITLTokenSecret([]byte("dev-secret"), os.Stderr),
		agentflow.WithRecorder(recorder),
		agentflow.WithEventSink(eventSink),
	)
	if tracer != nil {
		opts = append(opts, agentflow.WithTracer(tracer))
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	queue := agentflow.NewInMemoryJobQueue()
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
		WorkerID:    "example-worker",
		Concurrency: 2,
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := "127.0.0.1:8080"

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
		fmt.Printf("HTTP server listening on %s (metrics at /metrics)\n", addr)
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
