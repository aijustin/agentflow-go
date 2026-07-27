package runtime

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// countingTool records how many times it actually executed and lingers long
// enough for a whole batch to reach the governance gate concurrently.
type countingTool struct {
	calls *atomic.Int32
	delay time.Duration
}

func (t countingTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	t.calls.Add(1)
	select {
	case <-ctx.Done():
		return core.ToolResult{}, ctx.Err()
	case <-time.After(t.delay):
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

// The doom-loop limit is a check-then-act on a counter shared by the whole
// batch. Identical calls dispatched in parallel could all read the count
// before any of them recorded an attempt, so every one of them passed a gate
// meant to allow only the first.
func TestDoomLoopLimitHoldsAcrossParallelBatch(t *testing.T) {
	var calls atomic.Int32
	same := json.RawMessage(`{"query":"loop"}`)
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo", Input: same},
			{ID: "c2", Name: "echo", Input: same},
			{ID: "c3", Name: "echo", Input: same},
			{ID: "c4", Name: "echo", Input: same},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 16)
	// limit 2 allows one attempt: the second sees a prior attempt and is denied.
	scenario.Runtime.DoomLoopLimit = 2
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": countingTool{calls: &calls, delay: 40 * time.Millisecond}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-doom-parallel", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("doom-loop limit 2 must permit exactly 1 identical call in a batch, got %d", got)
	}
}

// A per-run rate cap counts every call to the tool regardless of input, so a
// parallel batch must not be able to overshoot it either.
func TestRateCapHoldsAcrossParallelBatch(t *testing.T) {
	var calls atomic.Int32
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo", Input: json.RawMessage(`{"query":"a"}`)},
			{ID: "c2", Name: "echo", Input: json.RawMessage(`{"query":"b"}`)},
			{ID: "c3", Name: "echo", Input: json.RawMessage(`{"query":"c"}`)},
			{ID: "c4", Name: "echo", Input: json.RawMessage(`{"query":"d"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 16)
	tool := scenario.Tools["echo"]
	tool.RateCap = 2
	scenario.Tools["echo"] = tool
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": countingTool{calls: &calls, delay: 40 * time.Millisecond}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-ratecap-parallel", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got > 2 {
		t.Fatalf("rate cap 2 must not be exceeded by a parallel batch, got %d calls", got)
	}
}

// Distinct inputs under a doom-loop limit are not repeats, so they must keep
// running concurrently rather than being serialized by the governance gate.
func TestDoomLoopLimitKeepsDistinctInputsParallel(t *testing.T) {
	var inflight, max atomic.Int32
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo", Input: json.RawMessage(`{"query":"a"}`)},
			{ID: "c2", Name: "echo", Input: json.RawMessage(`{"query":"b"}`)},
			{ID: "c3", Name: "echo", Input: json.RawMessage(`{"query":"c"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 16)
	scenario.Runtime.DoomLoopLimit = 2
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": slowEchoTool{inflight: &inflight, max: &max, delay: 60 * time.Millisecond}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-doom-distinct", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if max.Load() < 2 {
		t.Fatalf("expected distinct inputs to stay parallel, max inflight=%d", max.Load())
	}
}
