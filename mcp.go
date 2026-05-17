package agentflow

import (
	"context"
	"net/http"

	mcphttp "github.com/aijustin/agentflow-go/internal/adapter/mcp/http"
	mcpstdio "github.com/aijustin/agentflow-go/internal/adapter/mcp/stdio"
	mcptool "github.com/aijustin/agentflow-go/internal/adapter/mcp/tool"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// NewMCPHTTPClient creates an MCP JSON-RPC client over HTTP.
func NewMCPHTTPClient(endpoint string, client *http.Client) (mcp.Client, error) {
	return mcphttp.NewClient(endpoint, client)
}

type MCPStdioClientConfig struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
}

type MCPStdioClient interface {
	mcp.Client
	Close() error
}

// NewMCPStdioClient creates an MCP JSON-RPC client over a child process stdio transport.
func NewMCPStdioClient(ctx context.Context, config MCPStdioClientConfig) (MCPStdioClient, error) {
	return mcpstdio.NewClient(ctx, mcpstdio.Config{Command: config.Command, Args: config.Args, Env: config.Env, Dir: config.Dir})
}

// NewMCPToolExecutor adapts one MCP server tool into an AgentFlow tool executor.
func NewMCPToolExecutor(client mcp.Client, tool string) (core.ToolExecutor, error) {
	return mcptool.NewExecutor(client, tool)
}
