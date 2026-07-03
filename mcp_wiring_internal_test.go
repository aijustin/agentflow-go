package agentflow

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

func TestMCPWiringOptionsCreatesHTTPClientFromServer(t *testing.T) {
	scenario := core.Scenario{
		Name: "mcp-http",
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant"},
		},
		MCP: core.MCPConfig{
			Servers: []core.MCPServer{{Name: "docs", Transport: "http", URL: "http://127.0.0.1:9/mcp"}},
		},
		Tools: map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool", Metadata: map[string]string{"mcp_tool": "search"}},
		},
	}
	opts, err := MCPWiringOptions(context.Background(), scenario, MCPRegistry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestMCPWiringOptionsInfersToolFromPrefix(t *testing.T) {
	scenario := core.Scenario{
		Name: "mcp-prefix",
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant"},
		},
		MCP: core.MCPConfig{
			Servers: []core.MCPServer{{Name: "docs", Transport: "http", URL: "http://127.0.0.1:9/mcp", ToolPrefix: "docs"}},
		},
		Tools: map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool"},
		},
	}
	opts, err := MCPWiringOptions(context.Background(), scenario, MCPRegistry{
		Clients: map[string]mcp.Client{"docs": stubMCPClient{}},
	})
	if err != nil || len(opts) != 1 {
		t.Fatalf("opts=%d err=%v", len(opts), err)
	}
}

func TestMCPClientForServerValidation(t *testing.T) {
	if _, err := mcpClientForServer(context.Background(), core.MCPServer{Transport: "http"}, nil); err == nil {
		t.Fatal("expected missing url error")
	}
	if _, err := mcpClientForServer(context.Background(), core.MCPServer{Transport: "stdio"}, nil); err == nil {
		t.Fatal("expected missing command error")
	}
	if _, err := mcpClientForServer(context.Background(), core.MCPServer{Transport: "grpc"}, nil); err == nil {
		t.Fatal("expected unsupported transport error")
	}
}

type stubMCPClient struct{}

func (stubMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (stubMCPClient) CallTool(context.Context, mcp.CallToolRequest) (mcp.CallToolResult, error) {
	return mcp.CallToolResult{}, nil
}
