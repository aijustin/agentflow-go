package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		principal, ok := identity.PrincipalFromContext(ctx)
		if !ok || strings.TrimSpace(principal.Scope.TenantID) == "" {
			return core.ToolResult{}, fmt.Errorf("agentflow: tenant-scoped retriever requires tenant identity")
		}
		var payload map[string]any
		if len(call.Input) > 0 {
			if err := json.Unmarshal(call.Input, &payload); err != nil {
				return core.ToolResult{}, fmt.Errorf("agentflow: decode tenant-scoped retriever input: %w", err)
			}
		}
		if payload == nil {
			payload = map[string]any{}
		}
		payload["namespace"] = tenantKnowledgeNamespace(t.namespace, principal.Scope.TenantID)
		raw, err := json.Marshal(payload)
		if err != nil {
			return core.ToolResult{}, err
		}
		call.Input = raw
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

	// Elicitor answers server requests for user input raised during a tool
	// call (MCP protocol 2026-07-28 multi round-trip requests). It is host
	// supplied because only the host knows whether asking the user means
	// rendering a form, pausing behind a human gate, or refusing. Servers are
	// only told the client can be asked when this is set.
	Elicitor mcp.Elicitor
}

// WireMCPTools binds scenario MCP servers to mcp.tool executors.
func WireMCPTools(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]agentflow.Option, error) {
	return MCPWiringOptions(ctx, scenario, registry)
}

// MCPWiringOptions returns Framework options that wire mcp.tool declarations to MCP servers.
func MCPWiringOptions(ctx context.Context, scenario core.Scenario, registry MCPRegistry) ([]agentflow.Option, error) {
	return mcpWiringOptions(ctx, scenario, registry, mcpClientForServer)
}

type mcpClientFactory func(context.Context, core.MCPServer, MCPRegistry) (mcp.Client, error)

func mcpWiringOptions(ctx context.Context, scenario core.Scenario, registry MCPRegistry, factory mcpClientFactory) (opts []agentflow.Option, err error) {
	clients := make(map[string]mcp.Client, len(registry.Clients))
	ownedClients := make([]mcp.Client, 0, len(scenario.MCP.Servers))
	defer func() {
		if err == nil || len(ownedClients) == 0 {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, closeOwnedMCPClients(cleanupCtx, ownedClients))
	}()
	for name, client := range registry.Clients {
		clients[name] = client
	}
	for _, server := range scenario.MCP.Servers {
		if _, exists := clients[server.Name]; exists {
			continue
		}
		client, err := factory(ctx, server, registry)
		if err != nil {
			return nil, fmt.Errorf("agentflow: mcp server %q: %w", server.Name, err)
		}
		clients[server.Name] = client
		ownedClients = append(ownedClients, client)
	}
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
	if len(ownedClients) > 0 {
		opts = append(opts, agentflow.WithCloser(func(closeCtx context.Context) error {
			return closeOwnedMCPClients(closeCtx, ownedClients)
		}))
	}
	return opts, nil
}

func closeOwnedMCPClients(ctx context.Context, clients []mcp.Client) error {
	errs := make([]error, 0, len(clients))
	for i := len(clients) - 1; i >= 0; i-- {
		client := clients[i]
		if session, ok := client.(mcp.SessionClient); ok {
			if err := session.Terminate(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if closer, ok := client.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func mcpClientForServer(ctx context.Context, server core.MCPServer, registry MCPRegistry) (mcp.Client, error) {
	options, err := mcpClientOptions(server)
	if err != nil {
		return nil, err
	}
	options.Elicitor = registry.Elicitor
	httpClient := registry.HTTPClient
	switch strings.ToLower(strings.TrimSpace(server.Transport)) {
	case "", "http":
		if strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("url is required for http transport")
		}
		return adapters.NewMCPHTTPClientWithOptions(server.URL, httpClient, options)
	case "stdio":
		if len(server.Command) == 0 {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		cfg := adapters.MCPStdioClientConfig{Command: server.Command[0], Options: options}
		if len(server.Command) > 1 {
			cfg.Args = append([]string(nil), server.Command[1:]...)
		}
		return adapters.NewMCPStdioClient(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported transport %q", server.Transport)
	}
}

func mcpClientOptions(server core.MCPServer) (mcp.ClientOptions, error) {
	switch strings.ToLower(strings.TrimSpace(server.Metadata["mcp_protocol_mode"])) {
	case "", string(mcp.ProtocolModeLegacy):
		return mcp.ClientOptions{Mode: mcp.ProtocolModeLegacy}, nil
	case string(mcp.ProtocolModeModern):
		return mcp.ClientOptions{Mode: mcp.ProtocolModeModern}, nil
	default:
		return mcp.ClientOptions{}, fmt.Errorf("unsupported metadata.mcp_protocol_mode %q", server.Metadata["mcp_protocol_mode"])
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
