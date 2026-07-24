package adapters

import (
	"context"
	"database/sql"
	stdslog "log/slog"
	"net/http"

	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	auditslog "github.com/aijustin/agentflow-go/internal/adapter/audit/slog"
	eventobs "github.com/aijustin/agentflow-go/internal/adapter/event/observability"
	eventslog "github.com/aijustin/agentflow-go/internal/adapter/event/slog"
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

// --- Event and Audit Sinks ---

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

func NewNoopAuditSink() audit.Sink {
	return audit.NoopSink()
}

func NewInMemoryAuditSink(limit int) audit.Sink {
	return auditinmem.NewSink(limit)
}

func NewFileAuditSink(path string) (audit.Sink, error) {
	return auditfile.NewSink(path)
}

// --- Event Stores and Hubs ---

type PostgresEventStoreConfig struct {
	DB              *sql.DB
	TableName       string
	SkipSchemaSetup bool
}

// PostgresOutboxEventSinkConfig configures NewPostgresOutboxEventSink.
type PostgresOutboxEventSinkConfig struct {
	DB *sql.DB
	// EventsTableName overrides the durable event table; see
	// PostgresEventStoreConfig.TableName.
	EventsTableName string
	// OutboxTableName overrides the fallback outbox table (default
	// agentflow_outbox, migration 0005). It must match the run-state
	// repository's outbox table when customized.
	OutboxTableName string
	SkipSchemaSetup bool
}

// NewPostgresOutboxEventSink creates the PostgreSQL event store and an
// outbox-backed event sink for it. The sink appends to the store first; when
// the durable append fails, the event is parked in the run-state outbox
// (single INSERT, same database as the run snapshots) and later redelivered
// by the framework's outbox relay with its minted sequence, so a transient
// store outage no longer loses lifecycle events. Live publishers (e.g. an
// EventHub) are notified exactly like with NewEventStoreSink on the success
// path.
//
// Wire the returned store into the framework with WithEventStore and start
// the relay with WithOutboxRelay, otherwise parked rows are never delivered:
//
//	store, sink, err := adapters.NewPostgresOutboxEventSink(ctx, cfg, eventHub)
//	fw, err := agentflow.New(scenario,
//		agentflow.WithEventSink(adapters.NewEventFanoutSink(sink, ...)),
//		agentflow.WithEventStore(store),
//		agentflow.WithOutboxRelay(0),
//		agentflow.WithRunStateRepository(pgRuns),
//	)
func NewPostgresOutboxEventSink(ctx context.Context, config PostgresOutboxEventSinkConfig, publishers ...observability.EventPublisher) (observability.EventStore, core.EventSink, error) {
	store, err := observabilitypostgres.NewStore(ctx, observabilitypostgres.Config{
		DB:              config.DB,
		TableName:       config.EventsTableName,
		SkipSchemaSetup: config.SkipSchemaSetup,
	})
	if err != nil {
		return nil, nil, err
	}
	sink, err := observabilitypostgres.NewOutboxSink(observabilitypostgres.OutboxSinkConfig{
		Store:           store,
		OutboxTableName: config.OutboxTableName,
		Publishers:      publishers,
	})
	if err != nil {
		return nil, nil, err
	}
	return store, sink, nil
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

// --- Prometheus ---

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

// --- OpenTelemetry ---

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
