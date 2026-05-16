package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

type Client struct {
	endpoint string
	client   *nethttp.Client
	nextID   atomic.Int64
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
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
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("mcp http: endpoint is required")
	}
	if client == nil {
		client = nethttp.DefaultClient
	}
	return &Client{endpoint: endpoint, client: client}, nil
}

func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	var decoded struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", nil, &decoded); err != nil {
		return nil, err
	}
	return decoded.Tools, nil
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

func (c *Client) call(ctx context.Context, method string, params json.RawMessage, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reqBody, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return err
	}
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp http: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("mcp http: rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if out == nil || len(decoded.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return err
	}
	return nil
}
