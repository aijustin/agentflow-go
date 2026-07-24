package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type transientToolError struct{ message string }

func (e transientToolError) Error() string   { return e.message }
func (e transientToolError) Retryable() bool { return true }

// idempotencyCaptureTool records the idempotency key of every execution and
// fails its first failFirst calls with a retryable error.
type idempotencyCaptureTool struct {
	mu        sync.Mutex
	keys      []string
	failFirst int
}

func (t *idempotencyCaptureTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = append(t.keys, core.IdempotencyKeyFromContext(ctx))
	if len(t.keys) <= t.failFirst {
		return core.ToolResult{}, transientToolError{message: "flaky tool"}
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

func (t *idempotencyCaptureTool) captured() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.keys...)
}

func newIdempotencyEngine(t *testing.T, tool core.ToolExecutor, events *captureEvents, scenario core.Scenario) *Engine {
	t.Helper()
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    newToolThenAnswerGateway(t),
		Tools:  mapToolRegistry{"echo": tool},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func newToolThenAnswerGateway(t *testing.T) *llmmock.Gateway {
	t.Helper()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
	})
	return gateway
}

// The autonomous path reuses the LLM-issued tool call ID as the idempotency
// key: {run_id}:{tool_call_id}. The same key must reach the executor context
// and the ToolCalled/ToolReturned event payloads.
func TestEngineToolIdempotencyKeyReachesExecutorAndEvents(t *testing.T) {
	tool := &idempotencyCaptureTool{}
	events := &captureEvents{}
	engine := newIdempotencyEngine(t, tool, events, toolScenario(core.ApprovalNever, core.SideEffectRead, 4))
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-idem", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	keys := tool.captured()
	if len(keys) != 1 || keys[0] != "run-idem:call-1" {
		t.Fatalf("expected executor idempotency key run-idem:call-1, got %v", keys)
	}
	var sawCalled, sawReturned bool
	for _, event := range events.events {
		if event.Type != core.EventToolCalled && event.Type != core.EventToolReturned {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["idempotency_key"] != "run-idem:call-1" {
			t.Fatalf("expected idempotency_key in %s payload, got %+v", event.Type, payload)
		}
		if event.Type == core.EventToolCalled {
			sawCalled = true
		} else {
			sawReturned = true
		}
	}
	if !sawCalled || !sawReturned {
		t.Fatalf("expected ToolCalled and ToolReturned events, got %+v", events.types())
	}
}

// In-memory retries of one tool execution (executeToolWithRetry) reuse the
// same logical call and therefore the same idempotency key.
func TestEngineToolRetryKeepsIdempotencyKey(t *testing.T) {
	tool := &idempotencyCaptureTool{failFirst: 1}
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Runtime.MaxRetries = 1
	engine := newIdempotencyEngine(t, tool, &captureEvents{}, scenario)
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-idem-retry", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	keys := tool.captured()
	if len(keys) != 2 {
		t.Fatalf("expected two attempts, got %v", keys)
	}
	if keys[0] != "run-idem-retry:call-1" || keys[1] != keys[0] {
		t.Fatalf("retry must reuse the same idempotency key, got %v", keys)
	}
}
