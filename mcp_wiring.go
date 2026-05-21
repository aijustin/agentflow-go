package agentflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// MCPRegistry supplies MCP clients for scenario server declarations.
type MCPRegistry struct {
	Clients    map[string]mcp.Client
	HTTPClient *http.Client
}

// WireMCPTools binds scenario MCP servers to mcp.tool executors.
func WireMCPTools(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]Option, error) {
	return MCPWiringOptions(ctx, scenario, registry)
}

// MCPWiringOptions returns Framework options that wire mcp.tool declarations to MCP servers.
func MCPWiringOptions(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]Option, error) {
	clients := make(map[string]mcp.Client, len(registry.Clients))
	for name, client := range registry.Clients {
		clients[name] = client
	}
	for _, server := range scenario.MCP.Servers {
		if _, exists := clients[server.Name]; exists {
			continue
		}
		client, err := mcpClientForServer(ctx, server, registry.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("agentflow: mcp server %q: %w", server.Name, err)
		}
		clients[server.Name] = client
	}
	var opts []Option
	for name, tool := range scenario.Tools {
		if tool.Type != "mcp.tool" {
			continue
		}
		serverName := strings.TrimSpace(tool.Metadata["mcp_server"])
		if serverName == "" && len(scenario.MCP.Servers) == 1 {
			serverName = scenario.MCP.Servers[0].Name
		}
		if serverName == "" {
			return nil, fmt.Errorf("agentflow: mcp tool %q requires metadata.mcp_server or a single scenario.mcp.servers entry", name)
		}
		client, ok := clients[serverName]
		if !ok {
			return nil, fmt.Errorf("agentflow: mcp tool %q references unknown server %q", name, serverName)
		}
		mcpTool := strings.TrimSpace(tool.Metadata["mcp_tool"])
		if mcpTool == "" {
			prefix := strings.TrimSpace(serverToolPrefix(scenario, serverName))
			if prefix != "" && strings.HasPrefix(name, prefix+".") {
				mcpTool = strings.TrimPrefix(name, prefix+".")
			}
		}
		if mcpTool == "" {
			return nil, fmt.Errorf("agentflow: mcp tool %q requires metadata.mcp_tool", name)
		}
		exec, err := NewMCPToolExecutor(client, mcpTool)
		if err != nil {
			return nil, err
		}
		opts = append(opts, WithToolExecutor(name, exec))
	}
	return opts, nil
}

func mcpClientForServer(ctx context.Context, server core.MCPServer, httpClient *http.Client) (mcp.Client, error) {
	switch strings.ToLower(strings.TrimSpace(server.Transport)) {
	case "", "http":
		if strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("url is required for http transport")
		}
		return NewMCPHTTPClient(server.URL, httpClient)
	case "stdio":
		if len(server.Command) == 0 {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		cfg := MCPStdioClientConfig{Command: server.Command[0]}
		if len(server.Command) > 1 {
			cfg.Args = append([]string(nil), server.Command[1:]...)
		}
		return NewMCPStdioClient(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported transport %q", server.Transport)
	}
}

func serverToolPrefix(scenario core.Scenario, serverName string) string {
	for _, server := range scenario.MCP.Servers {
		if server.Name == serverName {
			return server.ToolPrefix
		}
	}
	return ""
}
