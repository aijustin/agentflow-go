package observability_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestTraceParentPropagation(t *testing.T) {
	root := observability.WithTrace(context.Background(), "trace-1", "span-root")
	child := observability.WithTraceParent(root, "trace-1", "span-child", "span-root")

	traceID, spanID := observability.TraceFromContext(child)
	if traceID != "trace-1" || spanID != "span-child" {
		t.Fatalf("unexpected child trace context: %q %q", traceID, spanID)
	}
	if got := observability.ParentSpanFromContext(child); got != "span-root" {
		t.Fatalf("parent span = %q", got)
	}
}
