package agentflow

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestToolRegistryResolveToolRequiresName(t *testing.T) {
	reg := newToolRegistry(map[string]core.ToolExecutor{"echo": stubToolExecutor{}}, nil)
	if _, _, err := reg.ResolveTool(context.Background(), core.Tool{}); err == nil {
		t.Fatal("expected empty tool name error")
	}
	if _, ok, err := reg.ResolveTool(context.Background(), core.Tool{Name: "echo"}); err != nil || !ok {
		t.Fatalf("expected eager executor, ok=%v err=%v", ok, err)
	}
}

func TestToolRegistryResolveToolRejectsNilResolverResult(t *testing.T) {
	reg := newToolRegistry(nil, core.ToolResolverFunc(func(context.Context, core.Tool) (core.ToolExecutor, error) {
		return nil, nil
	}))
	_, _, err := reg.ResolveTool(context.Background(), core.Tool{Name: "lazy"})
	if err == nil {
		t.Fatal("expected nil executor error")
	}
}

func TestToolRegistryResolveToolCachesLazyExecutor(t *testing.T) {
	calls := 0
	reg := newToolRegistry(nil, core.ToolResolverFunc(func(context.Context, core.Tool) (core.ToolExecutor, error) {
		calls++
		return stubToolExecutor{}, nil
	}))
	for range 2 {
		if _, ok, err := reg.ResolveTool(context.Background(), core.Tool{Name: "lazy"}); err != nil || !ok {
			t.Fatalf("resolve failed: ok=%v err=%v", ok, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cached lazy executor, calls=%d", calls)
	}
}

func TestWorkflowAgentRegistryMissingAgent(t *testing.T) {
	reg := workflowAgentRegistry{agents: map[string]core.Agent{}}
	if _, ok := reg.Agent("missing"); ok {
		t.Fatal("expected missing agent")
	}
}

type stubToolExecutor struct{}

func (stubToolExecutor) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
