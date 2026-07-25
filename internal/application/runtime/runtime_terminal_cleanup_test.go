package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// TestTerminalCompletionClearsRunScopedState: once a run completes, the
// run-keyed in-memory bookkeeping (approval cache, deny breaker,
// interjection buffer) must not linger on a long-lived engine.
func TestTerminalCompletionClearsRunScopedState(t *testing.T) {
	repo := runstateinmem.NewRepository()
	store := toolorch.NewMemoryApprovalStore()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Runtime.HITLDenyLimit = 3
	engine, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           gateway,
		ApprovalStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"q":"x"}`)
	toolorch.RememberAllow(store, "run-clean", "echo", input)
	engine.denyBreaker.RecordDeny("run-clean")

	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-clean", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), repo, "run-clean")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed, got %s", snapshot.Status)
	}
	if _, ok := store.Get("run-clean", toolorch.Key("echo", input)); ok {
		t.Fatal("approval cache entry must be cleared at terminal state")
	}
	if tripped, count := engine.denyBreaker.RecordDeny("run-clean"); count != 1 || tripped {
		t.Fatalf("deny breaker must restart from zero after terminal cleanup, got count=%d tripped=%v", count, tripped)
	}
}

// TestClearRunScopedStateDropsInterjections covers the interjection buffer
// leg of the terminal cleanup.
func TestClearRunScopedStateDropsInterjections(t *testing.T) {
	repo := runstateinmem.NewRepository()
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-interject", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs: repo,
		LLM:  llmmock.NewGateway(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Interject("run-interject", "steer"); err != nil {
		t.Fatal(err)
	}
	if got := engine.interjections.PendingCount("run-interject"); got != 1 {
		t.Fatalf("expected buffered interjection, got %d", got)
	}
	engine.loadedToolsForRun("run-interject").add("deferred.tool")
	engine.markSelfCompactPending("run-interject")
	engine.clearRunScopedState("run-interject")
	if got := engine.interjections.PendingCount("run-interject"); got != 0 {
		t.Fatalf("interjection buffer must be cleared, got %d", got)
	}
	if _, ok := engine.loadedTools.Load("run-interject"); ok {
		t.Fatal("loaded tool state must be cleared")
	}
	if _, ok := engine.pendingSelfCompact.Load("run-interject"); ok {
		t.Fatal("pending self-compact state must be cleared")
	}
}

// TestInterjectRejectsUnknownOrInactiveRun: interjections for missing or
// terminal runs are rejected instead of being buffered forever.
func TestInterjectRejectsUnknownOrInactiveRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-done", ScenarioName: "scenario", Status: runstate.RunStatusCompleted,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs: repo,
		LLM:  llmmock.NewGateway(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Interject("run-missing", "steer"); err == nil {
		t.Fatal("expected error for unknown run")
	}
	if err := engine.Interject("run-done", "steer"); err == nil {
		t.Fatal("expected error for completed run")
	}
	if got := engine.interjections.PendingCount("run-missing"); got != 0 {
		t.Fatalf("rejected interjection must not be buffered, got %d", got)
	}
	if got := engine.interjections.PendingCount("run-done"); got != 0 {
		t.Fatalf("rejected interjection must not be buffered, got %d", got)
	}
}

// errorChunkStreamer fails every streaming tool call with a chunk carrying
// the given structured error, so retry classification on the streaming path
// can be observed through the number of calls.
type errorChunkStreamer struct {
	*llmmock.Gateway
	chunkErr error
	calls    int
}

func newErrorChunkStreamer(err error) *errorChunkStreamer {
	return &errorChunkStreamer{Gateway: llmmock.NewGateway(), chunkErr: err}
}

func (g *errorChunkStreamer) StreamChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (<-chan llm.ChatChunk, error) {
	g.calls++
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Done: true, Error: g.chunkErr.Error(), Err: g.chunkErr}
	close(ch)
	return ch, nil
}

// TestStreamingToolCallRetryClassification: a mid-stream provider error that
// carries its structured form (llm.APIError) is retried on the streaming
// path exactly like on the unary path; before, the error was flattened into
// a string and shouldRetry never fired.
func TestStreamingToolCallRetryClassification(t *testing.T) {
	gateway := newErrorChunkStreamer(llm.APIError{Provider: "mock", StatusCode: 429, Status: "429", Body: "slow down"})
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStream)
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	agent := scenario.Agents["assistant"]
	agent.Policy.RetryLimit = 2 // three attempts total
	engine, err := NewEngine(scenario, Dependencies{
		Runs: runstateinmem.NewRepository(),
		LLM:  gateway,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.chatWithToolsWithRetry(context.Background(), "run-retry-stream",
		agent, core.LLMProfileRef{}, llm.ToolCallRequest{}, gateway, 1, func(llm.ChatChunk) {})
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	var apiErr llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("structured provider error must be preserved, got %T: %v", err, err)
	}
	if gateway.calls != 3 {
		t.Fatalf("retryable streaming error must be retried up to max attempts, got %d calls", gateway.calls)
	}
}

// TestStreamingToolCallNonRetryableError: a 400-class streaming failure must
// not be retried.
func TestStreamingToolCallNonRetryableError(t *testing.T) {
	gateway := newErrorChunkStreamer(llm.APIError{Provider: "mock", StatusCode: 400, Status: "400", Body: "bad request"})
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStream)
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs: runstateinmem.NewRepository(),
		LLM:  gateway,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.chatWithToolsWithRetry(context.Background(), "run-no-retry-stream",
		toolScenario(core.ApprovalNever, core.SideEffectRead, 4).Agents["assistant"], core.LLMProfileRef{}, llm.ToolCallRequest{}, gateway, 1, func(llm.ChatChunk) {})
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if gateway.calls != 1 {
		t.Fatalf("non-retryable streaming error must not be retried, got %d calls", gateway.calls)
	}
}
