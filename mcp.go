package agentflow

import (
	"net/http"

	mcphttp "github.com/aijustin/agentflow-go/internal/adapter/mcp/http"
	mcptool "github.com/aijustin/agentflow-go/internal/adapter/mcp/tool"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// NewMCPHTTPClient creates an MCP JSON-RPC client over HTTP.
func NewMCPHTTPClient(endpoint string, client *http.Client) (mcp.Client, error) {
	return mcphttp.NewClient(endpoint, client)
}

// NewMCPToolExecutor adapts one MCP server tool into an AgentFlow tool executor.
func NewMCPToolExecutor(client mcp.Client, tool string) (core.ToolExecutor, error) {
	return mcptool.NewExecutor(client, tool)
}
