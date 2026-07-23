package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// --- Knowledge Wiring ---

// KnowledgeRegistry wires scenario knowledge collections to retriever executors.
type KnowledgeRegistry struct {
	Embedder llm.Embedder
	Store    knowledge.VectorStore
	Reranker knowledge.Reranker
}

// KnowledgeWiringOptions returns Framework options that bind scenario knowledge collections.
func KnowledgeWiringOptions(scenario core.Scenario, registry KnowledgeRegistry) ([]agentflow.Option, error) {
	if registry.Embedder == nil || registry.Store == nil {
		return nil, fmt.Errorf("agentflow: knowledge registry requires embedder and store")
	}
	var opts []agentflow.Option
	for _, collection := range scenario.Knowledge.Collections {
		toolName := strings.TrimSpace(collection.Tool)
		if toolName == "" {
			toolName = "knowledge." + collection.Name
		}
		if _, exists := scenario.Tools[toolName]; !exists {
			return nil, fmt.Errorf("agentflow: knowledge collection %q requires tool %q in scenario.tools", collection.Name, toolName)
		}
		profile := strings.TrimSpace(collection.EmbedProfile)
		if profile == "" {
			profile = firstEmbedProfile(scenario.LLMs)
		}
		if profile == "" {
			return nil, fmt.Errorf("agentflow: knowledge collection %q requires embed_profile", collection.Name)
		}
		mode := knowledge.SearchModeVector
		if strings.EqualFold(collection.SearchMode, "hybrid") {
			mode = knowledge.SearchModeHybrid
		}
		exec, err := adapters.NewRetrieverTool(adapters.RetrieverToolConfig{
			Embedder:     registry.Embedder,
			Store:        registry.Store,
			Profile:      profile,
			Namespace:    collection.Namespace,
			SearchMode:   mode,
			Reranker:     registry.Reranker,
			DefaultLimit: 5,
		})
		if err != nil {
			return nil, err
		}
		opts = append(opts, agentflow.WithToolExecutor(toolName, &tenantScopedRetriever{
			inner:        exec,
			tenantScoped: collection.TenantScoped,
			namespace:    collection.Namespace,
		}))
	}
	return opts, nil
}

type tenantScopedRetriever struct {
	inner        core.ToolExecutor
	tenantScoped bool
	namespace    string
}

func (t *tenantScopedRetriever) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.tenantScoped {
		if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
			var payload map[string]any
			if len(call.Input) > 0 {
				_ = json.Unmarshal(call.Input, &payload)
			}
			if payload == nil {
				payload = map[string]any{}
			}
			if _, ok := payload["namespace"]; !ok {
				payload["namespace"] = tenantKnowledgeNamespace(t.namespace, principal.Scope.TenantID)
				raw, err := json.Marshal(payload)
				if err != nil {
					return core.ToolResult{}, err
				}
				call.Input = raw
			}
		}
	}
	return t.inner.Execute(ctx, call)
}

func tenantKnowledgeNamespace(base, tenantID string) string {
	base = strings.TrimSpace(base)
	tenantID = strings.TrimSpace(tenantID)
	if base == "" {
		return tenantID
	}
	if tenantID == "" {
		return base
	}
	return tenantID + "/" + base
}

func firstEmbedProfile(profiles map[string]core.LLMProfileRef) string {
	for name, profile := range profiles {
		for _, cap := range profile.Capabilities {
			if cap == "embed" {
				return name
			}
		}
	}
	for name := range profiles {
		return name
	}
	return ""
}

// --- MCP Wiring ---

// MCPRegistry supplies MCP clients for scenario server declarations.
type MCPRegistry struct {
	Clients    map[string]mcp.Client
	HTTPClient *http.Client
}

// WireMCPTools binds scenario MCP servers to mcp.tool executors.
func WireMCPTools(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]agentflow.Option, error) {
	return MCPWiringOptions(ctx, scenario, registry)
}

// MCPWiringOptions returns Framework options that wire mcp.tool declarations to MCP servers.
func MCPWiringOptions(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]agentflow.Option, error) {
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
	var opts []agentflow.Option
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
		exec, err := adapters.NewMCPToolExecutor(client, mcpTool)
		if err != nil {
			return nil, err
		}
		opts = append(opts, agentflow.WithToolExecutor(name, exec))
	}
	return opts, nil
}

func mcpClientForServer(ctx context.Context, server core.MCPServer, httpClient *http.Client) (mcp.Client, error) {
	switch strings.ToLower(strings.TrimSpace(server.Transport)) {
	case "", "http":
		if strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("url is required for http transport")
		}
		return adapters.NewMCPHTTPClient(server.URL, httpClient)
	case "stdio":
		if len(server.Command) == 0 {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		cfg := adapters.MCPStdioClientConfig{Command: server.Command[0]}
		if len(server.Command) > 1 {
			cfg.Args = append([]string(nil), server.Command[1:]...)
		}
		return adapters.NewMCPStdioClient(ctx, cfg)
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
