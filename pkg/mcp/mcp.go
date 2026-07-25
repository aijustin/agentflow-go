package mcp

import (
	"context"
	"encoding/json"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ListToolsResult is the MCP tools/list response shape. TTLMs and CacheScope
// are optional forward-compatible cache hints for newer protocol revisions.
type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
	TTLMs      *int64 `json:"ttlMs,omitempty"`
	CacheScope string `json:"cacheScope,omitempty"`
}

type Content struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	Mime string          `json:"mimeType,omitempty"`
}

type CallToolRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content           []Content       `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// ProtocolVersion is the MCP protocol revision the framework clients
// negotiate during the initialize handshake.
const ProtocolVersion = "2026-07-28"

type Client interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, req CallToolRequest) (CallToolResult, error)
}

// SessionClient is an optional capability for clients that hold a server-side
// MCP session. Callers that manage long-lived clients should terminate the
// session instead of dropping the client silently, so servers can reclaim
// session state immediately instead of waiting for their idle TTL.
type SessionClient interface {
	Client
	// SessionID returns the session id assigned during initialize (empty when
	// the server is stateless or the handshake has not completed).
	SessionID() string
	// Terminate ends the server-side session and resets local handshake
	// state; the next call re-initializes. No-op without an active session.
	Terminate(ctx context.Context) error
}
