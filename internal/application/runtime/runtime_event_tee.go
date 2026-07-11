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

func eventTeeFromContext(ctx context.Context) core.EventSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(eventTeeKey{}).(core.EventSink)
	return sink
}
