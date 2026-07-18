package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestCompletionRequirementRecoversThenSucceeds(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	// First finish without the required tool.
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "almost done"}},
	})
	// After reminder, call complete_task then finish.
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "complete_task", Input: json.RawMessage(`{"ok":true}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectNone, 8)
	scenario.Tools["complete_task"] = core.Tool{
		Name: "complete_task", Approval: core.ApprovalNever, SideEffect: core.SideEffectNone,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = append(agent.Tools, "complete_task")
	agent.CompletionRequirement = &core.CompletionRequirement{
		Tool:     "complete_task",
		Reminder: "You must call complete_task",
		Recovery: &core.CompletionRecovery{MaxRetries: 2, BaseDelayMS: 0, MaxDelayMS: 0},
	}
	scenario.Agents["assistant"] = agent
	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}, "complete_task": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-complete", Agent: "assistant", Prompt: "finish work"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if events.count(core.EventCompletionRecovery) < 1 {
		t.Fatalf("expected CompletionRecovery event, got %+v", events.types())
	}
}

func TestCompletionRequirementFailsAfterRetries(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "no tool"}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "still no"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectNone, 8)
	scenario.Tools["complete_task"] = core.Tool{
		Name: "complete_task", Approval: core.ApprovalNever, SideEffect: core.SideEffectNone,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = append(agent.Tools, "complete_task")
	agent.CompletionRequirement = &core.CompletionRequirement{
		Tool:     "complete_task",
		Reminder: "call it",
		Recovery: &core.CompletionRecovery{MaxRetries: 1, BaseDelayMS: 0},
	}
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}, "complete_task": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-fail-complete", Agent: "assistant", Prompt: "go"})
	if err == nil || !strings.Contains(err.Error(), "completion requirement not satisfied") {
		t.Fatalf("expected completion requirement error, got %v", err)
	}
}
