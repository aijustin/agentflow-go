package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

type selfCompactGateway struct {
	steps int
}

func (selfCompactGateway) Supports(string, llm.Capability) bool { return true }

func (selfCompactGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *selfCompactGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	g.steps++
	switch g.steps {
	case 1:
		return llm.ToolCallResponse{
			ToolCalls: []llm.ToolCall{{
				ID: "1", Name: toolcatalog.ToolCompactContext,
				Input: json.RawMessage(`{"reason":"sub-task done"}`),
			}},
		}, nil
	default:
		return llm.ToolCallResponse{
			ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
		}, nil
	}
}

func TestEngineSelfCompactMetaTool(t *testing.T) {
	scenario := core.Scenario{
		Name: "self-compact",
		LLMs: map[string]core.LLMProfileRef{
			"default": {
				Provider: "mock",
				Model:    "test",
				Context:  contextwindow.Policy{ObservationMaskAfterTurns: 1},
			},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Description: "Echo"},
		},
	}
	gw := &selfCompactGateway{}
	engine, err := runtime.NewEngine(scenario, runtime.Dependencies{
		LLM:  gw,
		Runs: runstateinmem.NewRepository(),
		Tools: stubToolRegistry{
			"echo": execTool{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), runtime.RunRequest{RunID: "run-compact", Agent: "assistant", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if gw.steps < 2 {
		t.Fatalf("expected compact_context then final answer, steps=%d", gw.steps)
	}
}

func TestEngineSelfCompactWithCatalog(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{Name: "echo", Description: "Echo", Pin: true},
	})
	scenario := core.Scenario{
		Name: "self-compact-catalog",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Description: "Echo"},
		},
	}
	gw := &selfCompactGateway{}
	engine, err := runtime.NewEngine(scenario, runtime.Dependencies{
		LLM:           gw,
		Runs:          runstateinmem.NewRepository(),
		ToolCatalog:   catalog,
		DeferredTools: true,
		Tools: stubToolRegistry{
			"echo": execTool{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), runtime.RunRequest{RunID: "run-compact-catalog", Agent: "assistant", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
}
