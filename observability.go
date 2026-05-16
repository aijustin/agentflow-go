package agentflow

import (
	stdslog "log/slog"

	auditslog "github.com/aijustin/agentflow-go/internal/adapter/audit/slog"
	eventslog "github.com/aijustin/agentflow-go/internal/adapter/event/slog"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func NewSlogEventSink(logger *stdslog.Logger) core.EventSink {
	return eventslog.NewSink(logger)
}

func NewSlogAuditSink(logger *stdslog.Logger) audit.Sink {
	return auditslog.NewSink(logger)
}
