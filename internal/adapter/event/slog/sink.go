package slog

import (
	"context"
	stdslog "log/slog"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type Option func(*Sink)

type Sink struct {
	logger         *stdslog.Logger
	now            func() time.Time
	includePayload bool
}

func NewSink(logger *stdslog.Logger, opts ...Option) *Sink {
	if logger == nil {
		logger = stdslog.Default()
	}
	sink := &Sink{logger: logger, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		if opt != nil {
			opt(sink)
		}
	}
	return sink
}

func WithPayload() Option {
	return func(sink *Sink) { sink.includePayload = true }
}

func (sink *Sink) Emit(ctx context.Context, event core.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = sink.now().UTC()
	}
	attrs := []any{
		"event_type", string(event.Type),
		"run_id", event.RunID,
		"scenario_name", event.ScenarioName,
		"event_timestamp", event.Timestamp.Format(time.RFC3339Nano),
		"trace_id", event.TraceID,
		"span_id", event.SpanID,
	}
	if sink.includePayload && len(event.Payload) > 0 {
		attrs = append(attrs, "payload", string(event.Payload))
	}
	sink.logger.InfoContext(ctx, "agentflow runtime event", attrs...)
	return nil
}
