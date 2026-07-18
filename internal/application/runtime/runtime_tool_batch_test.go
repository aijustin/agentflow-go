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
