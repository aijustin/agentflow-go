package runtime

import (
	"context"

	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// ContextWithEventTee attaches a side-channel EventSink used by Framework.StreamRun
// to observe runtime events without requiring an EventHub subscription.
func ContextWithEventTee(ctx context.Context, sink core.EventSink) context.Context {
	return emit.ContextWithEventTee(ctx, sink)
}

// EventTeeFromContext returns the side-channel sink attached by
// ContextWithEventTee, or nil. The emission pipeline consults it so engine,
// workflow-runner, and facade emissions all reach a StreamRun tee.
func EventTeeFromContext(ctx context.Context) core.EventSink {
	return emit.EventTeeFromContext(ctx)
}
