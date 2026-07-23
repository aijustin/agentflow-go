package agentflow_test

import (
	"context"
	"encoding/json"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

type stubMCPClient struct{}

func (stubMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (stubMCPClient) CallTool(_ context.Context, req mcp.CallToolRequest) (mcp.CallToolResult, error) {
	return mcp.CallToolResult{StructuredContent: json.RawMessage(`{"tool":` + jsonString(req.Name) + `}`)}, nil
}

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestMCPWiringOptionsSingleServerInfersMetadata(t *testing.T) {
	scenario := core.Scenario{
		Name: "mcp-single",
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
	opts, err := httpx.MCPWiringOptions(context.Background(), scenario, httpx.MCPRegistry{
		Clients: map[string]mcp.Client{"docs": stubMCPClient{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
	fw, err := agentflow.New(
		scenario,
		append(opts, agentflow.WithLLMGateway(stubGateway{}))...,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fw == nil {
		t.Fatal("expected framework with mcp tool wiring")
	}
}

func TestWireMCPToolsDelegatesToMCPWiringOptions(t *testing.T) {
	scenario := core.Scenario{
		Name: "mcp-delegate",
		MCP: core.MCPConfig{
			Servers: []core.MCPServer{{Name: "docs", Transport: "http", URL: "http://127.0.0.1:9/mcp"}},
		},
		Tools: map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool", Metadata: map[string]string{"mcp_tool": "search"}},
		},
	}
	opts, err := httpx.WireMCPTools(context.Background(), scenario, httpx.MCPRegistry{
		Clients: map[string]mcp.Client{"docs": stubMCPClient{}},
	})
	if err != nil || len(opts) != 1 {
		t.Fatalf("WireMCPTools: opts=%d err=%v", len(opts), err)
	}
}
