package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// staleInjectingRepo fails the next `stale` Save calls with ErrStaleSnapshot
// (or every call when failAll is set), simulating an optimistic-concurrency
// storm on the run snapshot.
type staleInjectingRepo struct {
	runstate.Repository
	mu      sync.Mutex
	stale   int
	failAll bool
	saves   int
}

func (r *staleInjectingRepo) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	r.mu.Lock()
	r.saves++
	if r.failAll {
		r.mu.Unlock()
		return runstate.ErrStaleSnapshot
	}
	if r.stale > 0 {
		r.stale--
		r.mu.Unlock()
		return runstate.ErrStaleSnapshot
	}
	r.mu.Unlock()
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

func (r *staleInjectingRepo) saveCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saves
}

func seedRunningRun(t *testing.T, repo runstate.Repository, runID string) {
	t.Helper()
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: runID, ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func newBatchEngine(t *testing.T, repo runstate.Repository) *Engine {
	t.Helper()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "delegated answer"}})
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// TestToolBatchPersistsOutputsInSingleSave: under an injected stale-snapshot
// storm the batch must absorb conflicts in ONE compare-and-swap round
// (retrying that single save), never surfacing "after stale snapshot
// retries" on any tool result, and persist every output key.
func TestToolBatchPersistsOutputsInSingleSave(t *testing.T) {
	inner := runstateinmem.NewRepository()
	seedRunningRun(t, inner, "run-batch")
	repo := &staleInjectingRepo{Repository: inner, stale: 3}
	engine := newBatchEngine(t, repo)

	calls := []llm.ToolCall{
		{ID: "c1", Name: "echo", Input: json.RawMessage(`{"q":"1"}`)},
		{ID: "c2", Name: "echo", Input: json.RawMessage(`{"q":"2"}`)},
		{ID: "c3", Name: "echo", Input: json.RawMessage(`{"q":"3"}`)},
		{ID: "c4", Name: "echo", Input: json.RawMessage(`{"q":"4"}`)},
	}
	agent := engine.scenario.Agents["assistant"]
	items, err := engine.executeToolBatch(context.Background(), "run-batch", agent, core.LLMProfileRef{}, calls, newToolCallTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if strings.Contains(item.result.Error, "after stale snapshot retries") {
			t.Fatalf("item %d must not surface CAS-storm retries: %q", i, item.result.Error)
		}
		if item.result.Error != "" {
			t.Fatalf("item %d unexpectedly errored: %q", i, item.result.Error)
		}
	}
	// The whole batch persists in a single CAS round: 3 injected conflicts +
	// the one successful save, nothing per item.
	if got := repo.saveCalls(); got != 4 {
		t.Fatalf("expected 4 save calls for the whole batch (3 retries + 1 success), got %d", got)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), inner, "run-batch")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if _, ok := snapshot.StepOutputs["tool."+call.ID]; !ok {
			t.Fatalf("missing persisted step output for %s (have %v)", call.ID, snapshot.StepOutputs)
		}
	}
}

// TestSaveStepOutputsRetriesThenFailsBounded pins the retry contract of the
// single-CAS-round save: bounded stale conflicts are absorbed, an
// unbounded storm fails with the classified error.
func TestSaveStepOutputsRetriesThenFailsBounded(t *testing.T) {
	inner := runstateinmem.NewRepository()
	seedRunningRun(t, inner, "run-cas")
	repo := &staleInjectingRepo{Repository: inner, stale: 4}
	engine := newBatchEngine(t, repo)
	outputs := map[string]any{"tool.c1": core.ToolResult{Tool: "echo"}}
	if err := engine.saveStepOutputs(context.Background(), "run-cas", outputs); err != nil {
		t.Fatalf("expected stale conflicts within the retry bound to be absorbed, got %v", err)
	}

	repo.failAll = true
	err := engine.saveStepOutputs(context.Background(), "run-cas", outputs)
	if err == nil || !strings.Contains(err.Error(), "after stale snapshot retries") {
		t.Fatalf("expected bounded-retry failure, got %v", err)
	}
}

// TestToolBatchAnnotatesItemsWhenPersistFails: when the batch-level save
// fails, successful tools get the persist error on their result (visible to
// the model) while already-errored tools keep their original error.
func TestToolBatchAnnotatesItemsWhenPersistFails(t *testing.T) {
	inner := runstateinmem.NewRepository()
	seedRunningRun(t, inner, "run-persist-fail")
	repo := &staleInjectingRepo{Repository: inner, failAll: true}
	engine := newBatchEngine(t, repo)

	calls := []llm.ToolCall{
		{ID: "c1", Name: "echo", Input: json.RawMessage(`{"q":"1"}`)},
		{ID: "c2", Name: "echo", Input: json.RawMessage(`{"q":"2"}`)},
	}
	agent := engine.scenario.Agents["assistant"]
	items, err := engine.executeToolBatch(context.Background(), "run-persist-fail", agent, core.LLMProfileRef{}, calls, newToolCallTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if !strings.HasPrefix(item.result.Error, "persist tool output: ") {
			t.Fatalf("item %d must carry the persist failure, got %q", i, item.result.Error)
		}
		// The annotation must reach the LLM-facing message, matching the
		// inline persist-failure semantics of the single-call path.
		if !strings.Contains(item.message.Content, "persist tool output") {
			t.Fatalf("item %d message must surface the persist failure to the model, got %q", i, item.message.Content)
		}
	}
}

// TestToolBatchPersistKeyCoversDelegation: delegated calls persist under the
// sub-agent namespace, plain tools under tool.<callID>.
func TestToolBatchPersistKeyCoversDelegation(t *testing.T) {
	inner := runstateinmem.NewRepository()
	seedRunningRun(t, inner, "run-delegate-batch")
	engine := newBatchEngine(t, &staleInjectingRepo{Repository: inner})

	scenario := engine.scenario
	scenario.Agents["helper"] = core.Agent{Name: "helper", LLM: "default"}
	agent := scenario.Agents["assistant"]
	agent.SubAgents = []string{"helper"}

	calls := []llm.ToolCall{
		{ID: "c1", Name: "echo", Input: json.RawMessage(`{"q":"1"}`)},
		{ID: "c2", Name: "delegate_helper", Input: json.RawMessage(`{"prompt":"help me"}`)},
	}
	items, err := engine.executeToolBatch(context.Background(), "run-delegate-batch", agent, core.LLMProfileRef{}, calls, newToolCallTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if item.result.Error != "" {
			t.Fatalf("item %d unexpectedly errored: %q", i, item.result.Error)
		}
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), inner, "run-delegate-batch")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["tool.c1"]; !ok {
		t.Fatalf("missing tool.c1 step output (have %v)", snapshot.StepOutputs)
	}
	if _, ok := snapshot.StepOutputs["agent.helper.c2"]; !ok {
		t.Fatalf("missing agent.helper.c2 step output (have %v)", snapshot.StepOutputs)
	}
}
