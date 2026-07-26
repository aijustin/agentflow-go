package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// DefaultMaxResponseBytes caps how much of an RPC response body is read,
// so a misbehaving or compromised MCP server cannot exhaust memory by
// returning an unbounded response.
const DefaultMaxResponseBytes int64 = 16 << 20

// DefaultTimeout bounds a single RPC when the caller does not supply a client.
// An MCP server that accepts the connection and then stalls would otherwise
// hold the tool call — and the run — open forever.
const DefaultTimeout = 60 * time.Second

const sseScanBuf = 4 * 1024 * 1024

type Client struct {
	endpoint         string
	client           *nethttp.Client
	nextID           atomic.Int64
	maxResponseBytes int64
	options          mcp.ClientOptions

	// initMu serializes the initialize handshake; sessionID/ready are only
	// written under it and read under the same lock, so concurrent first
	// calls cannot double-initialize or race on the session header.
	initMu    sync.Mutex
	sessionID string
	ready     bool
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewClient(endpoint string, client *nethttp.Client) (*Client, error) {
	return NewClientWithOptions(endpoint, client, mcp.ClientOptions{})
}

// NewClientWithOptions creates an HTTP MCP client in an explicit protocol
// mode. The zero-value options retain the legacy initialize/session behavior.
func NewClientWithOptions(endpoint string, client *nethttp.Client, options mcp.ClientOptions) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("mcp http: endpoint is required")
	}
	if client == nil {
		client = &nethttp.Client{Timeout: DefaultTimeout}
	}
	version := core.FrameworkVersion()
	normalized, err := mcp.NormalizeClientOptions(options, version)
	if err != nil {
		return nil, err
	}
	return &Client{endpoint: endpoint, client: client, maxResponseBytes: DefaultMaxResponseBytes, options: normalized}, nil
}

// maxListToolsPages bounds nextCursor pagination so a misbehaving server
// cannot keep the client listing forever.
const maxListToolsPages = 100

func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	var all []mcp.Tool
	var cursor string
	for page := 0; ; page++ {
		var params json.RawMessage
		if cursor != "" {
			raw, err := json.Marshal(map[string]string{"cursor": cursor})
			if err != nil {
				return nil, err
			}
			params = raw
		}
		var decoded mcp.ListToolsResult
		if err := c.call(ctx, "tools/list", params, &decoded); err != nil {
			return nil, err
		}
		all = append(all, decoded.Tools...)
		if decoded.NextCursor == "" {
			return all, nil
		}
		cursor = decoded.NextCursor
		if page+1 >= maxListToolsPages {
			return nil, fmt.Errorf("mcp http: tools/list exceeded %d pages", maxListToolsPages)
		}
	}
}

func (c *Client) CallTool(ctx context.Context, req mcp.CallToolRequest) (mcp.CallToolResult, error) {
	if strings.TrimSpace(req.Name) == "" {
		return mcp.CallToolResult{}, fmt.Errorf("mcp http: tool name is required")
	}
	params, err := json.Marshal(req)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	var result mcp.CallToolResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return mcp.CallToolResult{}, err
	}
	return result, nil
}

// SessionID returns the MCP session id assigned during initialize (empty until
// the handshake has completed).
func (c *Client) SessionID() string {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	return c.sessionID
}

// Terminate ends the server-side session (HTTP DELETE per the MCP streamable
// HTTP transport) and resets local handshake state so the next call
// re-initializes. It is a no-op when no session was established.
func (c *Client) Terminate(ctx context.Context) error {
	if c.options.Mode == mcp.ProtocolModeModern {
		return nil
	}
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.sessionID == "" {
		c.ready = false
		return nil
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodDelete, c.endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	c.sessionID = ""
	c.ready = false
	if resp.StatusCode >= 400 && resp.StatusCode != nethttp.StatusNotFound && resp.StatusCode != nethttp.StatusMethodNotAllowed {
		return fmt.Errorf("mcp http: terminate session: unexpected status %s", resp.Status)
	}
	return nil
}

// errSessionGone marks a 404 on a session-scoped request: the server forgot
// our session (restart or idle TTL), so the client must re-handshake.
var errSessionGone = errors.New("mcp http: session expired")

func (c *Client) call(ctx context.Context, method string, params json.RawMessage, out any) error {
	if c.options.Mode == mcp.ProtocolModeModern {
		return c.rpc(ctx, method, params, out)
	}
	if err := c.ensureInitialized(ctx); err != nil {
		return err
	}
	err := c.rpc(ctx, method, params, out)
	if errors.Is(err, errSessionGone) {
		// Re-handshake once with a fresh session instead of failing the call.
		c.initMu.Lock()
		c.sessionID = ""
		c.ready = false
		c.initMu.Unlock()
		if err := c.ensureInitialized(ctx); err != nil {
			return err
		}
		return c.rpc(ctx, method, params, out)
	}
	return err
}

// ensureInitialized performs the MCP handshake once: initialize, capture the
// Mcp-Session-Id response header, then the initialized notification.
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.ready {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	params, err := json.Marshal(map[string]any{
		"protocolVersion": mcp.ProtocolVersionLegacy,
		"capabilities":    map[string]any{},
		"clientInfo":      c.options.ClientInfo,
	})
	if err != nil {
		return err
	}
	sessionID, err := c.rpcLocked(ctx, "initialize", params, nil)
	if err != nil {
		return fmt.Errorf("mcp http: initialize: %w", err)
	}
	c.sessionID = sessionID
	if err := c.notifyLocked(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp http: initialized notification: %w", err)
	}
	c.ready = true
	return nil
}

func (c *Client) rpc(ctx context.Context, method string, params json.RawMessage, out any) error {
	c.initMu.Lock()
	sessionID := c.sessionID
	c.initMu.Unlock()
	_, err := c.roundTrip(ctx, method, params, sessionID, out)
	return err
}

// rpcLocked is rpc for the initialize path: initMu is already held, and the
// response's Mcp-Session-Id header is returned to the caller.
func (c *Client) rpcLocked(ctx context.Context, method string, params json.RawMessage, out any) (string, error) {
	return c.roundTrip(ctx, method, params, "", out)
}

func mcpToolNameFromParams(method string, params json.RawMessage) string {
	if method != "tools/call" || len(params) == 0 {
		return ""
	}
	var call struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return ""
	}
	return strings.TrimSpace(call.Name)
}

func (c *Client) roundTrip(ctx context.Context, method string, params json.RawMessage, sessionID string, out any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	params, err := mcp.AddModernRequestMetadata(params, c.options)
	if err != nil {
		return "", err
	}
	id := c.nextID.Add(1)
	reqBody, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return "", err
	}
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	protocolVersion, err := mcp.ProtocolVersionForMode(c.options.Mode)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("MCP-Protocol-Version", protocolVersion)
	httpReq.Header.Set("Mcp-Method", method)
	if toolName := mcpToolNameFromParams(method, params); toolName != "" {
		httpReq.Header.Set("Mcp-Name", toolName)
	}
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == nethttp.StatusNotFound && sessionID != "" {
		return "", errSessionGone
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mcp http: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(body)) > c.maxResponseBytes {
		return "", fmt.Errorf("mcp http: response exceeds max bytes")
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, err = extractSSEResponse(body, id)
		if err != nil {
			return "", err
		}
	}
	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("mcp http: rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if out != nil && len(decoded.Result) > 0 {
		if err := json.Unmarshal(decoded.Result, out); err != nil {
			return "", err
		}
	}
	return resp.Header.Get("Mcp-Session-Id"), nil
}

// notifyLocked sends a JSON-RPC notification (no id, no response expected).
// initMu must be held.
func (c *Client) notifyLocked(ctx context.Context, method string, params json.RawMessage) error {
	raw, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersionLegacy)
	req.Header.Set("Mcp-Method", method)
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 && resp.StatusCode != nethttp.StatusAccepted {
		return fmt.Errorf("mcp http: notification: unexpected status %s", resp.Status)
	}
	return nil
}

var _ mcp.SessionClient = (*Client)(nil)

// extractSSEResponse pulls JSON-RPC messages out of an SSE body and returns
// the one whose id matches the request.
//
// Servers interleave progress notifications with the result, so the stream is
// matched on id and nothing else. Falling back to the last message when no id
// matched would let a notification, or a response to a different request, be
// decoded as this call's result: the caller cannot tell the difference, and a
// tool result is attacker-influenced data that goes straight into the model's
// context. An unmatched stream is an error.
func extractSSEResponse(body []byte, wantID int64) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, sseScanBuf), sseScanBuf)
	seen := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		raw := []byte(payload)
		seen++
		var probe struct {
			ID *int64 `json:"id"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.ID != nil && *probe.ID == wantID {
			return raw, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp http: read SSE stream: %w", err)
	}
	if seen == 0 {
		return nil, fmt.Errorf("mcp http: empty SSE stream")
	}
	return nil, fmt.Errorf("mcp http: SSE stream carried no response for request id %d", wantID)
}
