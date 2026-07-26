package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// ProtocolMode selects one MCP protocol era explicitly. Clients never
// auto-negotiate or fall back between eras: a deployment must choose the mode
// implemented by its server.
type ProtocolMode string

const (
	// ProtocolModeLegacy uses the stateful initialize/session protocol.
	ProtocolModeLegacy ProtocolMode = "legacy"
	// ProtocolModeModern uses the stateless per-request metadata protocol.
	ProtocolModeModern ProtocolMode = "modern"

	ProtocolVersionLegacy = "2025-11-25"
	ProtocolVersionModern = "2026-07-28"

	// ProtocolVersion is retained for source compatibility and names the
	// default protocol used by existing constructors.
	ProtocolVersion = ProtocolVersionLegacy
)

// Implementation identifies an MCP implementation on the wire.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientOptions configures the MCP protocol era and modern request metadata.
// The zero value selects legacy mode.
type ClientOptions struct {
	Mode               ProtocolMode
	ClientInfo         Implementation
	ClientCapabilities map[string]any

	// Elicitor answers server requests for user input during a tool call.
	// Setting one declares the elicitation capability, which a server must
	// see before it is allowed to ask. Multi round-trip input requires
	// ProtocolModeModern.
	Elicitor Elicitor

	// MaxInputRounds bounds how many times one tool call may be sent back for
	// more input. Zero uses DefaultMaxInputRounds.
	MaxInputRounds int
}

// NormalizeClientOptions validates options and fills deterministic defaults.
func NormalizeClientOptions(options ClientOptions, defaultVersion string) (ClientOptions, error) {
	switch options.Mode {
	case "", ProtocolModeLegacy:
		options.Mode = ProtocolModeLegacy
	case ProtocolModeModern:
	default:
		return ClientOptions{}, fmt.Errorf("mcp: unsupported protocol mode %q", options.Mode)
	}
	if strings.TrimSpace(options.ClientInfo.Name) == "" {
		options.ClientInfo.Name = "agentflow-go"
	}
	if strings.TrimSpace(options.ClientInfo.Version) == "" {
		options.ClientInfo.Version = defaultVersion
	}
	if options.ClientInfo.Version == "" {
		options.ClientInfo.Version = "dev"
	}
	if options.ClientCapabilities == nil {
		options.ClientCapabilities = map[string]any{}
	}
	if options.MaxInputRounds <= 0 {
		options.MaxInputRounds = DefaultMaxInputRounds
	}
	// A server may only send an input request the client declared support
	// for, so the capability has to track whether an Elicitor exists. It is
	// derived rather than caller-supplied to keep the two from drifting: a
	// declared capability with no handler would strand the server waiting on
	// an answer that can never come.
	if options.Elicitor != nil {
		options.ClientCapabilities["elicitation"] = map[string]any{}
	} else {
		delete(options.ClientCapabilities, "elicitation")
	}
	return options, nil
}

// ProtocolVersionForMode returns the wire revision for an explicit mode.
func ProtocolVersionForMode(mode ProtocolMode) (string, error) {
	switch mode {
	case "", ProtocolModeLegacy:
		return ProtocolVersionLegacy, nil
	case ProtocolModeModern:
		return ProtocolVersionModern, nil
	default:
		return "", fmt.Errorf("mcp: unsupported protocol mode %q", mode)
	}
}

// AddModernRequestMetadata merges the metadata required by the stateless MCP
// protocol into an object-shaped params payload.
func AddModernRequestMetadata(params json.RawMessage, options ClientOptions) (json.RawMessage, error) {
	if options.Mode != ProtocolModeModern {
		return params, nil
	}
	body := map[string]any{}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &body); err != nil {
			return nil, fmt.Errorf("mcp: modern request params must be an object: %w", err)
		}
		if body == nil {
			body = map[string]any{}
		}
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["io.modelcontextprotocol/protocolVersion"] = ProtocolVersionModern
	meta["io.modelcontextprotocol/clientCapabilities"] = options.ClientCapabilities
	meta["io.modelcontextprotocol/clientInfo"] = options.ClientInfo
	body["_meta"] = meta
	merged, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode modern request metadata: %w", err)
	}
	return merged, nil
}

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
