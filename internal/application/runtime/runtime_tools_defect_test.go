package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type okEchoTool struct{}

func (okEchoTool) Execute(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"ok":true}`)}, nil
}

type blockingTool struct{}

func (blockingTool) Execute(ctx context.Context, _ core.ToolCall) (core.ToolResult, error) {
	<-ctx.Done()
	return core.ToolResult{}, ctx.Err()
}

// T3: with ValidateToolInput enabled, a call whose input violates the tool's
// InputSchema is denied before execution.
func TestEngineValidatesToolInputWhenEnabled(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}})

	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Runtime.ValidateToolInput = true
	tool := scenario.Tools["echo"]
	tool.InputSchema = json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`)
	scenario.Tools["echo"] = tool

	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": okEchoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-validate", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	if !events.has(core.EventToolDenied) {
		t.Fatalf("expected tool denied on invalid input, got %+v", events.types())
	}
	if events.has(core.EventToolCalled) {
		t.Fatalf("invalid input must not reach execution, got %+v", events.types())
	}
}

// T3 default: with validation disabled (default), the same invalid input is not
// blocked and the tool executes.
func TestEngineToolInputValidationDisabledByDefault(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}})

	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	tool := scenario.Tools["echo"]
	tool.InputSchema = json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`)
	scenario.Tools["echo"] = tool

	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": okEchoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-novalidate", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	if !events.has(core.EventToolCalled) {
		t.Fatalf("expected tool to execute by default, got %+v", events.types())
	}
}

// T1: a per-tool Timeout bounds a single execution; a tool that never returns
// hits the deadline and the run fails with DeadlineExceeded.
func TestEngineAppliesPerToolTimeout(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})

	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	tool := scenario.Tools["echo"]
	tool.Timeout = 20 * time.Millisecond
	scenario.Tools["echo"] = tool

	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": blockingTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-timeout", Agent: "assistant", Prompt: "use echo"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from per-tool timeout, got %v", err)
	}
}

// F2: with a human gate configured, approval=always pauses (matching workflow
// ToolApprovalPauseRequired), instead of soft-denying and continuing the loop.
func TestEngineAlwaysApprovalPausesWhenGateConfigured(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"x"}`)}},
	})
	scenario := toolScenario(core.ApprovalAlways, core.SideEffectExternal, 4)
	gate := &capturingGate{repo: repo}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:      repo,
		LLM:       gateway,
		Tools:     mapToolRegistry{"echo": okEchoTool{}},
		HumanGate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-always-pause", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token == "" {
		t.Fatalf("expected always-approval tool to pause, got %+v", result)
	}
}

// F6: write/external/dangerous tools must not auto-retry on transient errors.
func TestEngineDoesNotRetryWriteSideEffectTools(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectWrite, 4)
	scenario.Runtime.MaxRetries = 2
	tool := &flakyTool{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-no-write-retry", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("write side-effect tool must not auto-retry, got %d calls", tool.calls)
	}
}
