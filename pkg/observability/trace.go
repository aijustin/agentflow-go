package observability

import "context"

type traceContextKey struct{}

type traceContext struct {
	traceID string
	spanID  string
}

// WithTrace returns a new context carrying the given trace and span IDs.
// The IDs are automatically copied into core.Event fields by the runtime's
// emit path when this context is propagated through Run / RunHybrid / Stream.
func WithTrace(ctx context.Context, traceID, spanID string) context.Context {
	return context.WithValue(ctx, traceContextKey{}, traceContext{traceID: traceID, spanID: spanID})
}

// TraceFromContext returns the trace and span IDs stored in ctx by WithTrace.
// Both values are empty strings when no trace context is present.
func TraceFromContext(ctx context.Context) (traceID, spanID string) {
	if tc, ok := ctx.Value(traceContextKey{}).(traceContext); ok {
		return tc.traceID, tc.spanID
	}
	return "", ""
}
