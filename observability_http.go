package agentflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	stdslog "log/slog"
	"net/http"
	"sync/atomic"

	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	auditslog "github.com/aijustin/agentflow-go/internal/adapter/audit/slog"
	eventobs "github.com/aijustin/agentflow-go/internal/adapter/event/observability"
	eventslog "github.com/aijustin/agentflow-go/internal/adapter/event/slog"
	observabilityhttp "github.com/aijustin/agentflow-go/internal/adapter/observability/http"
	observabilityinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	observabilitypostgres "github.com/aijustin/agentflow-go/internal/adapter/observability/postgres"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/observability"
	oteladapter "github.com/aijustin/agentflow-go/pkg/observability/otel"
	promrecorder "github.com/aijustin/agentflow-go/pkg/observability/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// --- Observability Sinks, Stores, and UI ---

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
	// StudioSavePath enables POST /observability/api/studio/save for the configured scenario file.
	StudioSavePath string
	// TraceExploreURL is an optional trace UI link template, e.g. https://jaeger.example.com/trace/{trace_id}.
	TraceExploreURL string
	// InsecureAllowNoAuth disables the default-deny guard on mutating
	// endpoints (HITL resume, resume-from-step/checkpoint, fork, studio
	// run/save) when AuthMiddleware is nil. Only set it behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
	// Logger receives the one-time construction warning emitted when
	// AuthMiddleware is nil; nil discards it.
	Logger log.Logger
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
		Store:               config.Store,
		Hub:                 config.Hub,
		AuthMiddleware:      config.AuthMiddleware,
		TraceExploreURL:     config.TraceExploreURL,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
		Logger:              config.Logger,
	}
	if config.Framework != nil {
		adapter := &studioFramework{framework: config.Framework, savePath: config.StudioSavePath}
		httpConfig.Steps = adapter
		httpConfig.HITLResume = adapter
		httpConfig.Graph = adapter
		httpConfig.Resume = adapter
		httpConfig.History = adapter
		httpConfig.Checkpoints = adapter
		httpConfig.Restore = adapter
		httpConfig.Studio = adapter
		httpConfig.Codegen = adapter
		httpConfig.YAML = adapter
		httpConfig.ImportYAML = adapter
		httpConfig.RunStudio = adapter
		if config.StudioSavePath != "" {
			httpConfig.StudioSave = adapter
		}
		httpConfig.Compare = adapter
		httpConfig.Thread = adapter
		httpConfig.Fork = adapter
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

// --- Studio Framework Adapter ---

type studioFramework struct {
	framework *Framework
	savePath  string
}

func (adapter *studioFramework) ListRunSteps(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunSteps(ctx, runID)
}

func (adapter *studioFramework) ResumeRunHITL(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (any, error) {
	return adapter.framework.ResumeRunByID(ctx, runID, decision, amendment, continueExecution)
}

func (adapter *studioFramework) ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error) {
	return adapter.framework.ResumeFromStep(ctx, runID, nodeID)
}

func (adapter *studioFramework) ListRunCheckpoints(ctx context.Context, runID string, limit int) (any, error) {
	return adapter.framework.ListRunCheckpoints(ctx, runID, limit)
}

func (adapter *studioFramework) GetRunCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.GetRunCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ResumeFromCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ExportScenarioGraph() any {
	return adapter.framework.ExportScenarioGraph()
}

func (adapter *studioFramework) ValidateStudioGraph(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.ValidateStudioGraph(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioBuilderCode(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioBuilderCode(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioScenarioYAML(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioScenarioYAML(ctx, graph)
}

func (adapter *studioFramework) ImportStudioScenarioYAML(ctx context.Context, yamlData []byte, layout any) (any, error) {
	var layoutGraph graph.ScenarioGraph
	if layout != nil {
		var err error
		layoutGraph, err = decodeStudioGraph(layout)
		if err != nil {
			return nil, err
		}
	}
	return adapter.framework.ImportStudioScenarioYAML(ctx, yamlData, layoutGraph)
}

func (adapter *studioFramework) RunStudioGraph(ctx context.Context, edited any, req any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	runReq, err := decodeStudioRunRequest(req)
	if err != nil {
		return nil, err
	}
	return adapter.framework.RunStudioGraph(ctx, graph, runReq)
}

func decodeStudioRunRequest(value any) (RunRequest, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return RunRequest{}, fmt.Errorf("studio run request: encode: %w", err)
	}
	var req RunRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return RunRequest{}, fmt.Errorf("studio run request: decode: %w", err)
	}
	return req, nil
}

func (adapter *studioFramework) CompareRuns(ctx context.Context, runA, runB string) (any, error) {
	return adapter.framework.CompareRuns(ctx, runA, runB)
}

func (adapter *studioFramework) ListRunThread(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunThread(ctx, runID)
}

func (adapter *studioFramework) ForkRun(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ForkRun(ctx, runID, version)
}

func (adapter *studioFramework) SaveStudioGraph(ctx context.Context, edited any) (any, error) {
	if adapter.savePath == "" {
		return nil, fmt.Errorf("studio save path is not configured")
	}
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.SaveStudioGraph(ctx, graph, adapter.savePath)
}

func decodeStudioGraph(value any) (graph.ScenarioGraph, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: encode: %w", err)
	}
	var out graph.ScenarioGraph
	if err := json.Unmarshal(raw, &out); err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: decode: %w", err)
	}
	return out, nil
}

// --- Audit Sinks ---

func NewNoopAuditSink() audit.Sink {
	return audit.NoopSink()
}

func NewInMemoryAuditSink(limit int) audit.Sink {
	return auditinmem.NewSink(limit)
}

func NewFileAuditSink(path string) (audit.Sink, error) {
	return auditfile.NewSink(path)
}

// --- Emit Failure Logging ---

// emitWarnGate prevents recursive Warn if the logger itself emits events.
var emitWarnGate atomic.Bool

func warnEmitFailure(logger log.Logger, ctx context.Context, runID string, err error) {
	if logger == nil || err == nil {
		return
	}
	if !emitWarnGate.CompareAndSwap(false, true) {
		return
	}
	defer emitWarnGate.Store(false)
	logger.Warn(ctx, "agentflow: event emit failed", "run_id", runID, "error", err)
}

// errorEmitFailure reports a lifecycle event that could not be delivered even
// after the bounded retries. Unlike warnEmitFailure it logs at error level:
// losing RunCompleted/RunPaused/RunFailed/RunCancelled corrupts downstream
// state tracking and must page an operator.
func errorEmitFailure(logger log.Logger, ctx context.Context, runID string, typ core.EventType, err error) {
	if logger == nil || err == nil {
		return
	}
	if !emitWarnGate.CompareAndSwap(false, true) {
		return
	}
	defer emitWarnGate.Store(false)
	logger.Error(ctx, "agentflow: lifecycle event emit failed after retries", "run_id", runID, "event_type", string(typ), "error", err)
}
