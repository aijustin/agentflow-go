package observability

import "context"

type MetricName string

const (
	MetricRuntimeEventsTotal         MetricName = "agentflow_runtime_events_total"
	MetricRunDurationSeconds         MetricName = "agentflow_run_duration_seconds"
	MetricToolDurationSeconds        MetricName = "agentflow_tool_duration_seconds"
	MetricQueueJobsTotal             MetricName = "agentflow_queue_jobs_total"
	MetricQueueJobsQueued            MetricName = "agentflow_queue_jobs_queued"
	MetricQueueJobsRunning           MetricName = "agentflow_queue_jobs_running"
	MetricQueueJobsDeadLetter        MetricName = "agentflow_queue_jobs_dead_letter"
	MetricMemoryTierRecords          MetricName = "agentflow_memory_tier_records"
	MetricMemoryTierMigrationsTotal  MetricName = "agentflow_memory_tier_migrations_total"
	MetricMemoryRecallLatencySeconds MetricName = "agentflow_memory_recall_latency_seconds"
	MetricLLMTokensTotal             MetricName = "agentflow_llm_tokens_total"
	MetricLLMDurationSeconds         MetricName = "agentflow_llm_duration_seconds"
	MetricLLMErrorsTotal             MetricName = "agentflow_llm_errors_total"
	MetricToolErrorsTotal            MetricName = "agentflow_tool_errors_total"
)

type SpanName string

const (
	SpanRuntimeEvent      SpanName = "agentflow.runtime.event"
	SpanRun               SpanName = "agentflow.run"
	SpanToolCall          SpanName = "agentflow.tool.call"
	SpanLLMCall           SpanName = "agentflow.llm.call"
	SpanMCPRPC            SpanName = "agentflow.mcp.rpc"
	SpanContextPrepare    SpanName = "agentflow.context.prepare"
	SpanQueueJob          SpanName = "agentflow.queue.job"
	SpanMemoryTierRecall  SpanName = "agentflow.memory.tier.recall"
	SpanMemoryTierMigrate SpanName = "agentflow.memory.tier.migrate"
)

type Attribute struct {
	Key   string
	Value string
}

type Recorder interface {
	IncCounter(ctx context.Context, name MetricName, attrs ...Attribute)
	// AddCounter adds a non-negative delta in one operation. Token and byte
	// totals must use this instead of issuing one IncCounter call per unit.
	AddCounter(ctx context.Context, name MetricName, value float64, attrs ...Attribute)
	ObserveHistogram(ctx context.Context, name MetricName, value float64, attrs ...Attribute)
	SetGauge(ctx context.Context, name MetricName, value float64, attrs ...Attribute)
}

type Span interface {
	RecordError(err error)
	SetAttributes(attrs ...Attribute)
	End()
}

type Tracer interface {
	Start(ctx context.Context, name SpanName, attrs ...Attribute) (context.Context, Span)
}

type RecorderFunc func(ctx context.Context, name MetricName, value float64, attrs ...Attribute)

func (fn RecorderFunc) IncCounter(ctx context.Context, name MetricName, attrs ...Attribute) {
	fn(ctx, name, 1, attrs...)
}

func (fn RecorderFunc) AddCounter(ctx context.Context, name MetricName, value float64, attrs ...Attribute) {
	fn(ctx, name, value, attrs...)
}

func (fn RecorderFunc) ObserveHistogram(context.Context, MetricName, float64, ...Attribute) {}

func (fn RecorderFunc) SetGauge(context.Context, MetricName, float64, ...Attribute) {}

type NoopRecorder struct{}

func (NoopRecorder) IncCounter(context.Context, MetricName, ...Attribute) {}

func (NoopRecorder) AddCounter(context.Context, MetricName, float64, ...Attribute) {}

func (NoopRecorder) ObserveHistogram(context.Context, MetricName, float64, ...Attribute) {}

func (NoopRecorder) SetGauge(context.Context, MetricName, float64, ...Attribute) {}

type NoopTracer struct{}

func (NoopTracer) Start(ctx context.Context, _ SpanName, _ ...Attribute) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) RecordError(error) {}

func (noopSpan) SetAttributes(...Attribute) {}

func (noopSpan) End() {}
