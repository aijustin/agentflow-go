package runtime

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestEnrichEventPayloadAddsWorkflowNodeID(t *testing.T) {
	ctx := core.ContextWithWorkflowNode(context.Background(), "review")
	payload := map[string]any{"tool": "echo"}
	enriched := enrichEventPayload(ctx, payload)
	m, ok := enriched.(map[string]any)
	if !ok || m["node_id"] != "review" {
		t.Fatalf("enriched = %#v", enriched)
	}
}

func TestEnrichEventPayloadPreservesExistingNodeID(t *testing.T) {
	ctx := core.ContextWithWorkflowNode(context.Background(), "review")
	payload := map[string]any{"node_id": "custom"}
	enriched := enrichEventPayload(ctx, payload).(map[string]any)
	if enriched["node_id"] != "custom" {
		t.Fatalf("node_id = %v", enriched["node_id"])
	}
}
