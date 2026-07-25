package httpx

import (
	"context"
	"net/http"
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
	if len(opts) != 2 {
		t.Fatalf("expected tool and owned-client closer options, got %d", len(opts))
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

type lifecycleMCPClient struct {
	terminated int
	closed     int
}

func (c *lifecycleMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (c *lifecycleMCPClient) CallTool(context.Context, mcp.CallToolRequest) (mcp.CallToolResult, error) {
	return mcp.CallToolResult{}, nil
}
func (c *lifecycleMCPClient) SessionID() string { return "session-1" }
func (c *lifecycleMCPClient) Terminate(context.Context) error {
	c.terminated++
	return nil
}
func (c *lifecycleMCPClient) Close() error {
	c.closed++
	return nil
}

func TestCloseOwnedMCPClientsTerminatesSessionsAndProcesses(t *testing.T) {
	client := &lifecycleMCPClient{}
	if err := closeOwnedMCPClients(context.Background(), []mcp.Client{client}); err != nil {
		t.Fatal(err)
	}
	if client.terminated != 1 || client.closed != 1 {
		t.Fatalf("expected terminate and close once, got terminated=%d closed=%d", client.terminated, client.closed)
	}
}

func TestMCPWiringOptionsClosesOwnedClientsOnValidationError(t *testing.T) {
	client := &lifecycleMCPClient{}
	scenario := core.Scenario{
		Name:   "mcp-cleanup",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant"}},
		MCP:    core.MCPConfig{Servers: []core.MCPServer{{Name: "docs", Transport: "http", URL: "http://example.test/mcp"}}},
		Tools:  map[string]core.Tool{"search": {Name: "search", Type: "mcp.tool"}},
	}
	_, err := mcpWiringOptions(context.Background(), scenario, MCPRegistry{}, func(context.Context, core.MCPServer, *http.Client) (mcp.Client, error) {
		return client, nil
	})
	if err == nil {
		t.Fatal("expected missing mcp_tool validation error")
	}
	if client.terminated != 1 || client.closed != 1 {
		t.Fatalf("owned client leaked on wiring error: terminated=%d closed=%d", client.terminated, client.closed)
	}
}
