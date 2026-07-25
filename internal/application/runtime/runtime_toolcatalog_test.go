package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/internal/application/runtime"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

type catalogGateway struct {
	steps []string
}

func (catalogGateway) Supports(string, llm.Capability) bool { return true }

func (g *catalogGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *catalogGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	names := make([]string, len(req.Tools))
	for i, spec := range req.Tools {
		names[i] = spec.Name
	}
	g.steps = append(g.steps, jsonMarshal(names))
	switch len(g.steps) {
	case 1:
		return llm.ToolCallResponse{
			ToolCalls: []llm.ToolCall{{
				ID: "1", Name: toolcatalog.ToolLoadSchemas,
				Input: json.RawMessage(`{"names":["docs.search"]}`),
			}},
		}, nil
	case 2:
		for _, name := range names {
			if name == "docs.search" {
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
				}, nil
			}
		}
	}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	}, nil
}

func jsonMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestEngineDeferredToolCatalogLoadsSchemas(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search docs"},
	})
	scenario := core.Scenario{
		Name: "catalog",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"docs.search", "echo"}},
		},
		Tools: map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool", Description: "Search docs"},
			"echo":        {Name: "echo", Type: "builtin.echo", Description: "Echo"},
		},
	}
	gw := &catalogGateway{}
	engine, err := runtime.NewEngine(scenario, runtime.Dependencies{
		LLM:  gw,
		Runs: runstateinmem.NewRepository(),
		Tools: stubToolRegistry{
			"docs.search": execTool{},
			"echo":        execTool{},
		},
		ToolCatalog:   catalog,
		DeferredTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), runtime.RunRequest{RunID: "run-catalog", Agent: "assistant", Prompt: "find docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.steps) < 2 {
		t.Fatalf("expected at least two LLM tool steps, got %v", gw.steps)
	}
	var first []string
	if err := json.Unmarshal([]byte(gw.steps[0]), &first); err != nil {
		t.Fatal(err)
	}
	if len(first) != 4 || first[0] != toolcatalog.ToolSearchTools || first[1] != toolcatalog.ToolLoadSchemas || first[2] != toolcatalog.ToolCompactContext || first[3] != "echo" {
		t.Fatalf("unexpected initial tools: %v", first)
	}
	var second []string
	if err := json.Unmarshal([]byte(gw.steps[1]), &second); err != nil {
		t.Fatal(err)
	}
	foundLoaded := false
	for _, name := range second {
		if name == "docs.search" {
			foundLoaded = true
		}
	}
	if !foundLoaded {
		t.Fatalf("loaded tool not advertised on second turn: %v", second)
	}
}

type execTool struct{}

func (execTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Output: json.RawMessage(`{"ok":true}`)}, nil
}

type stubToolRegistry map[string]core.ToolExecutor

func (r stubToolRegistry) ResolveTool(_ context.Context, tool core.Tool) (core.ToolExecutor, bool, error) {
	exec, ok := r[tool.Name]
	return exec, ok, nil
}
