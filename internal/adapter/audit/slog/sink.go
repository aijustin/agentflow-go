package slog

import (
	"context"
	stdslog "log/slog"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
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

func (sink *Sink) Record(ctx context.Context, event audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event = audit.CloneEvent(event).WithDefaults(sink.now())
	attrs := []any{
		"audit_type", string(event.Type),
		"event_timestamp", event.Timestamp.Format(time.RFC3339Nano),
		"principal_id", event.Principal.ID,
		"principal_type", string(event.Principal.Type),
		"tenant_id", event.Principal.Scope.TenantID,
		"action", string(event.Action),
		"resource_type", event.Resource.Type,
		"resource_id", event.Resource.ID,
		"resource_tenant_id", event.Resource.TenantID,
		"run_id", event.RunID,
		"outcome", event.Outcome,
		"reason", event.Reason,
	}
	if sink.includePayload && len(event.Payload) > 0 {
		attrs = append(attrs, "payload", string(event.Payload))
	}
	sink.logger.InfoContext(ctx, "agentflow audit event", attrs...)
	return nil
}
