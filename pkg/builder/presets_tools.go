package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

var (
	httpStatusInputSchema = json.RawMessage(`{"type":"object","required":["url"],"properties":{"method":{"type":"string","enum":["GET","HEAD"]},"url":{"type":"string"},"headers":{"type":"object","additionalProperties":{"type":"string"}}}}`)
	sqlQueryInputSchema   = json.RawMessage(`{"type":"object","required":["query_id"],"properties":{"query_id":{"type":"string"},"args":{"type":"array","items":{}}}}`)
	fsReadInputSchema     = json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	mcpSearchInputSchema  = json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`)
)

// MockToolLLM configures mock LLM with chat and tool_call capabilities.
func MockToolLLM() LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = LLMProviderMock
		p.Model = LLMModelMockTest
		p.Capabilities = []string{LLMCapChat, LLMCapToolCall}
	}
}

// HTTPToolStatusPreset configures the http.status tool for catalog ID http-tool.
func HTTPToolStatusPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeHTTP
		t.Description = "Read an allowlisted internal HTTP endpoint."
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
		t.RateCap = RateCapHTTPToolDefault
		t.InputSchema = httpStatusInputSchema
	}
}

// SQLQueryToolPreset configures the sql.query tool for catalog ID sql-tool.
func SQLQueryToolPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeSQL
		t.Description = "Run an allowlisted read-only SQL query."
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
		t.RateCap = RateCapSQLToolDefault
		t.InputSchema = sqlQueryInputSchema
	}
}

// FilesystemReadToolPreset configures the fs.read tool for catalog ID filesystem-tool.
func FilesystemReadToolPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeFilesystem
		t.Description = "Read a file under an allowlisted local root."
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
		t.RateCap = RateCapFilesystemToolDefault
		t.InputSchema = fsReadInputSchema
	}
}

// MCPSearchToolPreset configures the docs.search MCP tool for catalog ID mcp-tool.
func MCPSearchToolPreset(serverName string) ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeMCPTool
		t.Description = "Search documentation through an MCP server registered by the host application."
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
		if t.Metadata == nil {
			t.Metadata = make(map[string]string)
		}
		t.Metadata[MCPMetadataServer] = serverName
		t.Metadata[MCPMetadataTool] = MCPToolSearch
		t.InputSchema = mcpSearchInputSchema
	}
}

// MCPServerOption configures an MCP server declaration.
type MCPServerOption func(*core.MCPServer)

// MCPServerName sets the MCP server name.
func MCPServerName(name string) MCPServerOption {
	return func(s *core.MCPServer) {
		s.Name = name
	}
}

// MCPServerTransport sets the MCP transport.
func MCPServerTransport(transport string) MCPServerOption {
	return func(s *core.MCPServer) {
		s.Transport = transport
	}
}

// MCPServerURL sets the MCP HTTP URL.
func MCPServerURL(url string) MCPServerOption {
	return func(s *core.MCPServer) {
		s.URL = url
	}
}

// MCPServerToolPrefix sets the MCP tool prefix.
func MCPServerToolPrefix(prefix string) MCPServerOption {
	return func(s *core.MCPServer) {
		s.ToolPrefix = prefix
	}
}

// DocsMCPServerPreset configures the docs MCP server for catalog ID mcp-tool.
func DocsMCPServerPreset() []MCPServerOption {
	return []MCPServerOption{
		MCPServerName(NameMCPServerDocs),
		MCPServerTransport(MCPTransportHTTP),
		MCPServerURL(MCPServerURLDocsDefault),
		MCPServerToolPrefix(MCPToolPrefixDocs),
	}
}

// DefaultMockToolLLM registers mock LLM with chat and tool_call capabilities.
func (b *ScenarioBuilder) DefaultMockToolLLM() *ScenarioBuilder {
	return b.LLM(NameDefaultLLM, MockToolLLM())
}

// HTTPToolStatus registers the http.status tool declaration.
func (b *ScenarioBuilder) HTTPToolStatus() *ToolBuilder {
	return b.Tool(NameHTTPToolStatus, HTTPToolStatusPreset())
}

// SQLQueryTool registers the sql.query tool declaration.
func (b *ScenarioBuilder) SQLQueryTool() *ToolBuilder {
	return b.Tool(NameSQLQueryTool, SQLQueryToolPreset())
}

// FilesystemReadTool registers the fs.read tool declaration.
func (b *ScenarioBuilder) FilesystemReadTool() *ToolBuilder {
	return b.Tool(NameFilesystemReadTool, FilesystemReadToolPreset())
}

// MCPSearchTool registers the docs.search MCP tool declaration.
func (b *ScenarioBuilder) MCPSearchTool() *ToolBuilder {
	return b.Tool(NameMCPSearchTool, MCPSearchToolPreset(NameMCPServerDocs))
}

// MCPServer appends an MCP server declaration.
func (b *ScenarioBuilder) MCPServer(opts ...MCPServerOption) *ScenarioBuilder {
	b.commitAgent()
	server := core.MCPServer{}
	for _, opt := range opts {
		opt(&server)
	}
	b.s.MCP.Servers = append(b.s.MCP.Servers, server)
	return b
}

// DocsMCPServer registers the conventional docs MCP server.
func (b *ScenarioBuilder) DocsMCPServer() *ScenarioBuilder {
	return b.MCPServer(DocsMCPServerPreset()...)
}

// HTTPToolStatus wires http.status on the agent.
func (ab *AgentBuilder) HTTPToolStatus() *AgentBuilder {
	return ab.Tool(NameHTTPToolStatus)
}

// SQLQueryTool wires sql.query on the agent.
func (ab *AgentBuilder) SQLQueryTool() *AgentBuilder {
	return ab.Tool(NameSQLQueryTool)
}

// FilesystemReadTool wires fs.read on the agent.
func (ab *AgentBuilder) FilesystemReadTool() *AgentBuilder {
	return ab.Tool(NameFilesystemReadTool)
}

// MCPSearchTool wires docs.search on the agent.
func (ab *AgentBuilder) MCPSearchTool() *AgentBuilder {
	return ab.Tool(NameMCPSearchTool)
}

// ToolAssistantStack registers DefaultMockToolLLM for tool-enabled autonomous agents.
func (b *ScenarioBuilder) ToolAssistantStack() *ScenarioBuilder {
	return b.DefaultMockToolLLM()
}
