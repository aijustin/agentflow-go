package agentflow

import (
	"context"
	"errors"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type doneFrameStreamGateway struct {
	chunks []llm.ChatChunk
}

func (g *doneFrameStreamGateway) StreamChat(_ context.Context, _ string, _ llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	out := make(chan llm.ChatChunk, len(g.chunks))
	for _, chunk := range g.chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

func (g *doneFrameStreamGateway) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *doneFrameStreamGateway) ChatWithTools(_ context.Context, _ string, _ llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{}, nil
}

func (g *doneFrameStreamGateway) Supports(_ string, cap llm.Capability) bool {
	switch cap {
	case llm.CapChat, llm.CapStream, llm.CapToolCall:
		return true
	default:
		return false
	}
}

// deleteOnCompletedRepo deletes the snapshot after a Completed save so the
// StreamRun done-frame LoadAuthorized fails instead of inventing Completed.
type deleteOnCompletedRepo struct {
	inner *runstateinmem.Repository
}

func (r deleteOnCompletedRepo) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := r.inner.Save(ctx, snapshot, expectedVersion); err != nil {
		return err
	}
	if snapshot.Status == runstate.RunStatusCompleted {
		return r.inner.Delete(ctx, snapshot.RunID)
	}
	return nil
}

func (r deleteOnCompletedRepo) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	return r.inner.Load(ctx, runID)
}

func (r deleteOnCompletedRepo) Delete(ctx context.Context, runID string) error {
	return r.inner.Delete(ctx, runID)
}

func (r deleteOnCompletedRepo) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	return r.inner.List(ctx, filter)
}

func TestStreamRunDoneFrameErrorsWhenLoadFails(t *testing.T) {
	repo := deleteOnCompletedRepo{inner: runstateinmem.NewRepository()}
	scenario := core.Scenario{
		Name: "stream-done-load",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := New(
		scenario,
		WithLLMGateway(&doneFrameStreamGateway{chunks: []llm.ChatChunk{{Content: "x"}, {Done: true}}}),
		WithRunStateRepository(repo),
	)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := fw.StreamRun(context.Background(), RunRequest{
		RunID: "stream-done-missing", Agent: "assistant", Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawError bool
	var sawDone bool
	for frame := range frames {
		switch frame.Kind {
		case StreamFrameError:
			sawError = true
			if frame.Err == nil {
				t.Fatal("error frame missing err")
			}
		case StreamFrameDone:
			sawDone = true
			if frame.Result != nil && frame.Result.Status == runstate.RunStatusCompleted {
				t.Fatal("must not invent Completed when load fails")
			}
		}
	}
	if !sawError {
		t.Fatal("expected StreamFrameError when done-frame load fails")
	}
	if sawDone {
		t.Fatal("expected no done frame after load failure")
	}
}

func TestStreamRunDoneFrameEnforcesTenant(t *testing.T) {
	repo := runstateinmem.NewRepository()
	scenario := core.Scenario{
		Name: "stream-done-tenant",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := New(
		scenario,
		WithLLMGateway(&doneFrameStreamGateway{chunks: []llm.ChatChunk{{Content: "x"}, {Done: true}}}),
		WithRunStateRepository(repo),
	)
	if err != nil {
		t.Fatal(err)
	}
	tenantA := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "a", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-a"},
		Roles: []identity.Role{identity.RoleService},
	})
	frames, err := fw.StreamRun(tenantA, RunRequest{
		RunID: "stream-tenant-done", Agent: "assistant", Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range frames {
	}
	tenantB := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "b", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-b"},
		Roles: []identity.Role{identity.RoleService},
	})
	_, err = runstate.LoadAuthorized(tenantB, repo, "stream-tenant-done")
	if !errors.Is(err, runstate.ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch for cross-tenant load, got %v", err)
	}
}
