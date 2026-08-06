package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type slowEchoTool struct {
	inflight *atomic.Int32
	max      *atomic.Int32
	delay    time.Duration
}

func (t slowEchoTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	cur := t.inflight.Add(1)
	for {
		prev := t.max.Load()
		if cur <= prev || t.max.CompareAndSwap(prev, cur) {
			break
		}
	}
	defer t.inflight.Add(-1)
	select {
	case <-ctx.Done():
		return core.ToolResult{}, ctx.Err()
	case <-time.After(t.delay):
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

func TestDispatchToolCallsRunsBatchInParallel(t *testing.T) {
	var inflight, max atomic.Int32
	tool := slowEchoTool{inflight: &inflight, max: &max, delay: 80 * time.Millisecond}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo", Input: json.RawMessage(`{"query":"a"}`)},
			{ID: "c2", Name: "echo", Input: json.RawMessage(`{"query":"b"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-parallel", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if max.Load() < 2 {
		t.Fatalf("expected concurrent tool execution, max inflight=%d", max.Load())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected parallel batch under 200ms, got %s", elapsed)
	}
}

func TestDispatchToolCallsSerializesSamePath(t *testing.T) {
	var inflight, max atomic.Int32
	tool := slowEchoTool{inflight: &inflight, max: &max, delay: 60 * time.Millisecond}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo", Input: json.RawMessage(`{"path":"/same.go","query":"a"}`)},
			{ID: "c2", Name: "echo", Input: json.RawMessage(`{"path":"/same.go","query":"b"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-path-lock", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if max.Load() != 1 {
		t.Fatalf("expected same-path serialization, max inflight=%d", max.Load())
	}
}

func TestDoomLoopLimitDeniesRepeatedSameInput(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	same := json.RawMessage(`{"query":"loop"}`)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Input: same}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c2", Name: "echo", Input: same}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c3", Name: "echo", Input: same}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "stopped"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.DoomLoopLimit = 3
	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-doom", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "stopped" {
		t.Fatalf("expected run to finish after doom-loop deny, got %q", result.Output)
	}
	found := false
	for _, event := range events.events {
		if event.Type != core.EventToolDenied {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["kind"] == "doom_loop" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected doom_loop ToolDenied event, got %+v", events.types())
	}
}

// countingTool records how many times the executor actually ran and holds
// each execution open long enough for the whole parallel batch to overlap.
type countingTool struct {
	executed *atomic.Int32
	delay    time.Duration
}

func (t countingTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	t.executed.Add(1)
	select {
	case <-ctx.Done():
		return core.ToolResult{}, ctx.Err()
	case <-time.After(t.delay):
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

// DEFECT_REPORT D1: a parallel tool batch must not overrun the per-run
// budgets. N concurrent same-name calls against a cap of k must execute
// exactly k times; the rest must be denied before reaching the executor.
// The governance variant is the regression test for the residual race: the
// budget decision previously read committed-only counts, so every sibling
// in the batch observed the same pre-batch total and all were admitted.
func TestExecuteToolBatchEnforcesBudgetsUnderConcurrency(t *testing.T) {
	const batchSize = 6
	cases := []struct {
		name           string
		sameInput      bool
		configure      func(scenario *core.Scenario)
		policy         governance.ToolPolicy
		wantExecutions int32
	}{
		{
			name: "rate cap admits exactly k of a concurrent batch",
			configure: func(scenario *core.Scenario) {
				tool := scenario.Tools["echo"]
				tool.RateCap = 2
				scenario.Tools["echo"] = tool
			},
			wantExecutions: 2,
		},
		{
			// The doom-loop check fires on the limit-th repetition, so a
			// limit of 3 admits exactly 2 same-input calls.
			name:           "doom loop admits exactly limit-1 of a concurrent same-input batch",
			sameInput:      true,
			configure:      func(scenario *core.Scenario) { scenario.Runtime.DoomLoopLimit = 3 },
			wantExecutions: 2,
		},
		{
			name:           "governance tool budget admits exactly k of a concurrent batch",
			policy:         governance.NewToolBudgetPolicy(2),
			wantExecutions: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var executed atomic.Int32
			gateway := llmmock.NewGateway()
			gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
			calls := make([]llm.ToolCall, 0, batchSize)
			for i := range batchSize {
				input := json.RawMessage(fmt.Sprintf(`{"query":"q%d"}`, i))
				if tc.sameInput {
					input = json.RawMessage(`{"query":"same"}`)
				}
				calls = append(calls, llm.ToolCall{ID: fmt.Sprintf("c%d", i+1), Name: "echo", Input: input})
			}
			gateway.QueueToolCall("default", llm.ToolCallResponse{ToolCalls: calls})
			gateway.QueueToolCall("default", llm.ToolCallResponse{
				ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
			})
			scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
			if tc.configure != nil {
				tc.configure(&scenario)
			}
			engine, err := NewEngine(scenario, Dependencies{
				Runs:       runstateinmem.NewRepository(),
				LLM:        gateway,
				Tools:      mapToolRegistry{"echo": countingTool{executed: &executed, delay: 60 * time.Millisecond}},
				ToolPolicy: tc.policy,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Run(context.Background(), RunRequest{RunID: "run-budget-race", Agent: "assistant", Prompt: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Output != "done" {
				t.Fatalf("unexpected output %q", result.Output)
			}
			if got := executed.Load(); got != tc.wantExecutions {
				t.Fatalf("executor ran %d times, want exactly %d (budget must hold under concurrency)", got, tc.wantExecutions)
			}
		})
	}
}
