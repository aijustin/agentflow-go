package core

// MCPServer declares an MCP server wired into scenario tools at Framework build time.
type MCPServer struct {
	Name       string            `json:"name"`
	Transport  string            `json:"transport"` // stdio | http
	Command    []string          `json:"command,omitempty"`
	URL        string            `json:"url,omitempty"`
	ToolPrefix string            `json:"tool_prefix,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type MCPConfig struct {
	Servers []MCPServer `json:"servers,omitempty"`
}
