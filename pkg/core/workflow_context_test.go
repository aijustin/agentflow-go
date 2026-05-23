package core_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowNodeContext(t *testing.T) {
	ctx := core.ContextWithWorkflowNode(context.Background(), "review")
	if got := core.WorkflowNodeFromContext(ctx); got != "review" {
		t.Fatalf("WorkflowNodeFromContext = %q", got)
	}
	if got := core.WorkflowNodeFromContext(context.Background()); got != "" {
		t.Fatalf("empty context = %q", got)
	}
}
