package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

func TestClientListTools(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "tools/list" {
			t.Fatalf("unexpected method %q", req.Method)
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[{"name":"search","description":"Search docs","inputSchema":{"type":"object"}}]}`)})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "search" || tools[0].Description != "Search docs" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestClientCallTool(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "tools/call" {
			t.Fatalf("unexpected method %q", req.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatal(err)
		}
		if params["name"] != "search" {
			t.Fatalf("unexpected params: %+v", params)
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"structuredContent":{"answer":"ok"}}`)})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(context.Background(), mcp.CallToolRequest{Name: "search", Arguments: json.RawMessage(`{"query":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" || string(result.StructuredContent) != `{"answer":"ok"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientReturnsRPCError(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: 1, Error: &rpcError{Code: -32601, Message: "missing"}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("expected rpc error")
	}
}
