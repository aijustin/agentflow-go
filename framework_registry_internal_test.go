package agentflow

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestToolRegistryResolveToolRequiresName(t *testing.T) {
	reg := newToolRegistry(map[string]core.ToolExecutor{"echo": stubToolExecutor{}}, nil, defaultToolResolverCacheLimit)
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
	}), defaultToolResolverCacheLimit)
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
	}), defaultToolResolverCacheLimit)
	for range 2 {
		if _, ok, err := reg.ResolveTool(context.Background(), core.Tool{Name: "lazy"}); err != nil || !ok {
			t.Fatalf("resolve failed: ok=%v err=%v", ok, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cached lazy executor, calls=%d", calls)
	}
}

func TestToolRegistryScopesLazyExecutorCacheByPrincipal(t *testing.T) {
	calls := 0
	reg := newToolRegistry(nil, core.ToolResolverFunc(func(ctx context.Context, _ core.Tool) (core.ToolExecutor, error) {
		calls++
		principal, _ := identity.PrincipalFromContext(ctx)
		return scopedToolExecutor{tenantID: principal.Scope.TenantID}, nil
	}), defaultToolResolverCacheLimit)
	ctxA := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "service-a", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	ctxB := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "service-b", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-b"},
	})
	execA, ok, err := reg.ResolveTool(ctxA, core.Tool{Name: "lazy"})
	if err != nil || !ok {
		t.Fatalf("tenant A resolve failed: ok=%v err=%v", ok, err)
	}
	execB, ok, err := reg.ResolveTool(ctxB, core.Tool{Name: "lazy"})
	if err != nil || !ok {
		t.Fatalf("tenant B resolve failed: ok=%v err=%v", ok, err)
	}
	if execA.(scopedToolExecutor).tenantID != "tenant-a" || execB.(scopedToolExecutor).tenantID != "tenant-b" {
		t.Fatalf("tenant-scoped executors crossed cache boundary: A=%+v B=%+v", execA, execB)
	}
	if calls != 2 {
		t.Fatalf("expected one resolve per tenant, calls=%d", calls)
	}
	if _, _, err := reg.ResolveTool(ctxA, core.Tool{Name: "lazy"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("same principal should reuse its executor, calls=%d", calls)
	}
}

func TestToolRegistryEvictsLeastRecentlyUsedExecutor(t *testing.T) {
	calls := 0
	reg := newToolRegistry(nil, core.ToolResolverFunc(func(context.Context, core.Tool) (core.ToolExecutor, error) {
		calls++
		return stubToolExecutor{}, nil
	}), 2)
	for _, name := range []string{"a", "b", "a", "c", "b"} {
		if _, ok, err := reg.ResolveTool(context.Background(), core.Tool{Name: name}); err != nil || !ok {
			t.Fatalf("resolve %q failed: ok=%v err=%v", name, ok, err)
		}
	}
	if calls != 4 {
		t.Fatalf("expected b to be evicted after a was refreshed, calls=%d", calls)
	}
	if len(reg.cache) != 2 {
		t.Fatalf("cache exceeded configured bound: %d", len(reg.cache))
	}
}

func TestWithToolResolverCacheLimitRejectsNegativeValue(t *testing.T) {
	scenario := core.Scenario{
		Name:          "resolver-cache-limit",
		Agents:        map[string]core.Agent{"assistant": {Name: "assistant"}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	if _, err := New(scenario, WithToolResolverCacheLimit(-1)); err == nil {
		t.Fatal("expected negative cache limit error")
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

type scopedToolExecutor struct{ tenantID string }

func (scopedToolExecutor) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
