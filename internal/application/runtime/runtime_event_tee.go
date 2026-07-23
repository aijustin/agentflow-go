package runtime

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type eventTeeKey struct{}

// ContextWithEventTee attaches a side-channel EventSink used by Framework.StreamRun
// to observe runtime events without requiring an EventHub subscription.
func ContextWithEventTee(ctx context.Context, sink core.EventSink) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, eventTeeKey{}, sink)
}

// EventTeeFromContext returns the side-channel sink attached by
// ContextWithEventTee, or nil. The Framework-level event sink consults it so
// engine, workflow-runner, and facade emissions all reach a StreamRun tee.
func EventTeeFromContext(ctx context.Context) core.EventSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(eventTeeKey{}).(core.EventSink)
	return sink
}
