package orchestration

import (
	"context"
	"testing"
)

func TestWorkflowContextStepPrefix(t *testing.T) {
	ctx := withStepPrefix(context.Background(), "batch::")
	ctx = withStepPrefix(ctx, "item::")
	if got := stepPrefixFrom(ctx); got != "batch::item::" {
		t.Fatalf("prefix=%q", got)
	}
	if got := storageNodeID(ctx, "node"); got != "batch::item::node" {
		t.Fatalf("storage id=%q", got)
	}
	if got := bareNodeID("batch::item::node", "batch::item::"); got != "node" {
		t.Fatalf("bare=%q", got)
	}
}

func TestWorkflowContextSkipCurrentNode(t *testing.T) {
	ctx := withSkipCurrentNode(context.Background())
	if !skipCurrentNodeUpdate(ctx) {
		t.Fatal("expected skip current node")
	}
}
