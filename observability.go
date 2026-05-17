package agentflow

import (
	stdslog "log/slog"

	auditslog "github.com/aijustin/agentflow-go/internal/adapter/audit/slog"
	eventobs "github.com/aijustin/agentflow-go/internal/adapter/event/observability"
	eventslog "github.com/aijustin/agentflow-go/internal/adapter/event/slog"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

func NewSlogEventSink(logger *stdslog.Logger) core.EventSink {
	return eventslog.NewSink(logger)
}

func NewSlogAuditSink(logger *stdslog.Logger) audit.Sink {
	return auditslog.NewSink(logger)
}

func NewObservabilityEventSink(recorder observability.Recorder, tracer observability.Tracer, next core.EventSink) core.EventSink {
	return eventobs.NewSink(eventobs.Config{Recorder: recorder, Tracer: tracer, Next: next})
}
