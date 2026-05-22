package agentflow

import (
	"context"
	"database/sql"
	stdslog "log/slog"
	"net/http"

	auditslog "github.com/aijustin/agentflow-go/internal/adapter/audit/slog"
	eventobs "github.com/aijustin/agentflow-go/internal/adapter/event/observability"
	eventslog "github.com/aijustin/agentflow-go/internal/adapter/event/slog"
	observabilityhttp "github.com/aijustin/agentflow-go/internal/adapter/observability/http"
	observabilityinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	observabilitypostgres "github.com/aijustin/agentflow-go/internal/adapter/observability/postgres"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/observability"
	oteladapter "github.com/aijustin/agentflow-go/pkg/observability/otel"
	promrecorder "github.com/aijustin/agentflow-go/pkg/observability/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type PostgresEventStoreConfig struct {
	DB              *sql.DB
	TableName       string
	SkipSchemaSetup bool
}

type ObservabilityHTTPHandlerConfig struct {
	Store          observability.EventStore
	Hub            *observability.EventHub
	AuthMiddleware func(http.Handler) http.Handler
	// Framework enables Studio graph export, step listing, and resume-from-step.
	Framework *Framework
}

func NewSlogEventSink(logger *stdslog.Logger) core.EventSink {
	return eventslog.NewSink(logger)
}

// NewVerboseSlogEventSink logs runtime events with redacted-safe payload details to stderr-friendly sinks.
func NewVerboseSlogEventSink(logger *stdslog.Logger) core.EventSink {
	return eventslog.NewSink(logger, eventslog.WithPayload())
}

func NewSlogAuditSink(logger *stdslog.Logger) audit.Sink {
	return auditslog.NewSink(logger)
}

func NewObservabilityEventSink(recorder observability.Recorder, tracer observability.Tracer, next core.EventSink) core.EventSink {
	return eventobs.NewSink(eventobs.Config{Recorder: recorder, Tracer: tracer, Next: next})
}

func NewInMemoryEventStore() observability.EventStore {
	return observabilityinmem.NewStore()
}

func NewPostgresEventStore(ctx context.Context, config PostgresEventStoreConfig) (observability.EventStore, error) {
	return observabilitypostgres.NewStore(ctx, observabilitypostgres.Config{
		DB:              config.DB,
		TableName:       config.TableName,
		SkipSchemaSetup: config.SkipSchemaSetup,
	})
}

func NewEventHub() *observability.EventHub {
	return observability.NewEventHub()
}

func NewEventStoreSink(store observability.EventStore, publishers ...observability.EventPublisher) core.EventSink {
	return observability.NewEventStoreSink(store, publishers...)
}

func NewEventFanoutSink(sinks ...core.EventSink) core.EventSink {
	return observability.NewEventFanoutSink(sinks...)
}

func NewObservabilityHTTPHandler(config ObservabilityHTTPHandlerConfig) (http.Handler, error) {
	httpConfig := observabilityhttp.Config{
		Store:          config.Store,
		Hub:            config.Hub,
		AuthMiddleware: config.AuthMiddleware,
	}
	if config.Framework != nil {
		adapter := &studioFramework{framework: config.Framework}
		httpConfig.Steps = adapter
		httpConfig.Graph = adapter
		httpConfig.Resume = adapter
		httpConfig.History = adapter
		httpConfig.Checkpoints = adapter
		httpConfig.Restore = adapter
	}
	return observabilityhttp.NewHandler(httpConfig)
}

// PrometheusRecorder exposes in-process Prometheus text metrics for agentflow runtime signals.
type PrometheusRecorder = promrecorder.Recorder

// NewPrometheusRecorder creates a Prometheus-compatible observability recorder.
func NewPrometheusRecorder() *PrometheusRecorder {
	return promrecorder.NewRecorder()
}

// PrometheusMetricsHandler returns an http.Handler that serves recorder metrics.
func PrometheusMetricsHandler(recorder *PrometheusRecorder) http.Handler {
	return recorder.Handler()
}

// OpenTelemetryTracer adapts go.opentelemetry.io/otel/trace.Tracer to observability.Tracer.
type OpenTelemetryTracer = oteladapter.Tracer

// OpenTelemetryTracerProviderConfig configures a stdout-exporting TracerProvider for local development.
type OpenTelemetryTracerProviderConfig = oteladapter.TracerProviderConfig

// NewOpenTelemetryTracer wraps a host-configured OpenTelemetry tracer.
func NewOpenTelemetryTracer(tracer oteltrace.Tracer) observability.Tracer {
	return oteladapter.NewTracer(tracer)
}

// NewOpenTelemetryStdoutTracerProvider creates a TracerProvider that exports spans to stdout.
func NewOpenTelemetryStdoutTracerProvider(ctx context.Context, config OpenTelemetryTracerProviderConfig) (*sdktrace.TracerProvider, error) {
	return oteladapter.NewStdoutTracerProvider(ctx, config)
}

// OpenTelemetryTracerFromProvider returns a tracer backed by a TracerProvider.
func OpenTelemetryTracerFromProvider(provider *sdktrace.TracerProvider, instrumentationName string) observability.Tracer {
	return oteladapter.TracerFromProvider(provider, instrumentationName)
}
