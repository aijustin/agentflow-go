package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

func TestExecutorCallsMCPTool(t *testing.T) {
	client := &fakeClient{result: mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: "found"}}, StructuredContent: json.RawMessage(`{"answer":"found"}`)}}
	executor, err := NewExecutor(client, "search")
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{RunID: "run-1", Tool: "docs.search", Input: json.RawMessage(`{"query":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if client.called.Name != "search" || string(client.called.Arguments) != `{"query":"hello"}` {
		t.Fatalf("unexpected mcp call: %+v", client.called)
	}
	if result.Tool != "docs.search" || result.Error != "" || string(result.Output) == "" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestExecutorReturnsMCPErrorResult(t *testing.T) {
	client := &fakeClient{result: mcp.CallToolResult{IsError: true, Content: []mcp.Content{{Type: "text", Text: "denied"}}}}
	executor, err := NewExecutor(client, "danger")
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "danger"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "denied" {
		t.Fatalf("expected MCP error text, got %+v", result)
	}
}

type fakeClient struct {
	called mcp.CallToolRequest
	result mcp.CallToolResult
}

func (f *fakeClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }

func (f *fakeClient) CallTool(_ context.Context, req mcp.CallToolRequest) (mcp.CallToolResult, error) {
	f.called = req
	return f.result, nil
}
