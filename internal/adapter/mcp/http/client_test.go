package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// handshakeServer stubs a spec-compliant streamable-HTTP MCP server: it
// answers initialize (issuing a session id), accepts the initialized
// notification, and asserts that subsequent requests carry the session header.
type handshakeServer struct {
	t            *testing.T
	tools        []mcp.Tool
	callResult   mcp.CallToolResult
	sawSession   atomic.Bool
	terminated   atomic.Bool
	initializeCt atomic.Int64
	sse          bool
}

func (s *handshakeServer) handler() nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method == nethttp.MethodDelete {
			if r.Header.Get("Mcp-Session-Id") == "session-1" {
				s.terminated.Store(true)
			}
			w.WriteHeader(nethttp.StatusOK)
			return
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.t.Error(err)
			return
		}
		respond := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			raw, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: raw})
		}
		switch req.Method {
		case "initialize":
			s.initializeCt.Add(1)
			w.Header().Set("Mcp-Session-Id", "session-1")
			respond(map[string]any{
				"protocolVersion": mcp.ProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stub", "version": "0"},
			})
		case "notifications/initialized":
			w.WriteHeader(nethttp.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") == "session-1" {
				s.sawSession.Store(true)
			}
			raw, _ := json.Marshal(map[string]any{"tools": s.tools})
			if s.sse {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n\n", req.ID, raw)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: raw})
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") == "session-1" {
				s.sawSession.Store(true)
			}
			respond(s.callResult)
		default:
			s.t.Errorf("unexpected method %q", req.Method)
		}
	})
}

func TestClientListTools(t *testing.T) {
	stub := &handshakeServer{t: t, tools: []mcp.Tool{{Name: "search", Description: "Search docs", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	server := httptest.NewServer(stub.handler())
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
	if !stub.sawSession.Load() {
		t.Fatal("tools/list did not carry the session id negotiated during initialize")
	}
	if client.SessionID() != "session-1" {
		t.Fatalf("unexpected session id %q", client.SessionID())
	}
}

func TestClientListToolsSSE(t *testing.T) {
	stub := &handshakeServer{t: t, sse: true, tools: []mcp.Tool{{Name: "search"}}}
	server := httptest.NewServer(stub.handler())
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("unexpected tools from SSE response: %+v", tools)
	}
}

func TestClientCallTool(t *testing.T) {
	stub := &handshakeServer{t: t, callResult: mcp.CallToolResult{
		Content:           []mcp.Content{{Type: "text", Text: "ok"}},
		StructuredContent: json.RawMessage(`{"answer":"ok"}`),
	}}
	server := httptest.NewServer(stub.handler())
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

func TestClientRoundTripHeaders(t *testing.T) {
	var gotMethod, gotName, gotProtocol string
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		gotMethod = r.Header.Get("Mcp-Method")
		gotName = r.Header.Get("Mcp-Name")
		gotProtocol = r.Header.Get("MCP-Protocol-Version")
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"` + mcp.ProtocolVersion + `"}`)})
		case "notifications/initialized":
			w.WriteHeader(nethttp.StatusAccepted)
		case "tools/call":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(context.Background(), mcp.CallToolRequest{Name: "search"}); err != nil {
		t.Fatal(err)
	}
	if gotProtocol != mcp.ProtocolVersion {
		t.Fatalf("protocol header = %q, want %q", gotProtocol, mcp.ProtocolVersion)
	}
	if gotMethod != "tools/call" {
		t.Fatalf("method header = %q, want tools/call", gotMethod)
	}
	if gotName != "search" {
		t.Fatalf("name header = %q, want search", gotName)
	}
}

func TestClientTerminateEndsSessionAndReinitializes(t *testing.T) {
	stub := &handshakeServer{t: t, tools: []mcp.Tool{{Name: "search"}}}
	server := httptest.NewServer(stub.handler())
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Terminate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stub.terminated.Load() {
		t.Fatal("server did not observe the session DELETE")
	}
	if client.SessionID() != "" {
		t.Fatal("session id should be cleared after Terminate")
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stub.initializeCt.Load(); got != 2 {
		t.Fatalf("expected re-initialize after Terminate, initialize count = %d", got)
	}
}

func TestClientListToolsPagination(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"` + mcp.ProtocolVersion + `"}`)})
		case "notifications/initialized":
			w.WriteHeader(nethttp.StatusAccepted)
		case "tools/list":
			var params map[string]any
			_ = json.Unmarshal(req.Params, &params)
			var result string
			if params["cursor"] == "p2" {
				result = `{"tools":[{"name":"second"}]}`
			} else {
				result = `{"tools":[{"name":"search"}],"nextCursor":"p2"}`
			}
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(result)})
		}
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
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "second" {
		t.Fatalf("expected both pages, got %+v", tools)
	}
}

func TestClientRehandshakesWhenServerForgetsSession(t *testing.T) {
	var initializeCt atomic.Int64
	var acceptedSession atomic.Value // string; server only accepts this session id
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		switch req.Method {
		case "initialize":
			n := initializeCt.Add(1)
			session := fmt.Sprintf("session-%d", n)
			acceptedSession.Store(session)
			w.Header().Set("Mcp-Session-Id", session)
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"` + mcp.ProtocolVersion + `"}`)})
		case "notifications/initialized":
			w.WriteHeader(nethttp.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != acceptedSession.Load().(string) {
				w.WriteHeader(nethttp.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[{"name":"search"}]}`)})
		}
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
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	if got := initializeCt.Load(); got != 1 {
		t.Fatalf("initialize count = %d, want 1", got)
	}
	// Simulate a server restart: the client's session-1 is forgotten.
	acceptedSession.Store("forgotten")
	tools, err = client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("unexpected tools after re-handshake: %+v", tools)
	}
	if got := initializeCt.Load(); got != 2 {
		t.Fatalf("expected re-initialize after session 404, initialize count = %d", got)
	}
	if client.SessionID() != "session-2" {
		t.Fatalf("session id = %q, want session-2", client.SessionID())
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write(make([]byte, DefaultMaxResponseBytes+1))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("expected oversized response to be rejected")
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

func TestClientModernUsesStatelessRequestMetadata(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		requests.Add(1)
		if r.Method == nethttp.MethodDelete {
			t.Fatal("modern client must not terminate a protocol session")
		}
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method == "initialize" || req.Method == "notifications/initialized" {
			t.Fatalf("modern client sent legacy handshake method %q", req.Method)
		}
		if got := r.Header.Get("MCP-Protocol-Version"); got != mcp.ProtocolVersionModern {
			t.Fatalf("protocol header = %q", got)
		}
		if got := r.Header.Get("Mcp-Method"); got != req.Method {
			t.Fatalf("method header = %q, body method = %q", got, req.Method)
		}
		meta, ok := req.Params["_meta"].(map[string]any)
		if !ok {
			t.Fatalf("missing modern request metadata: %+v", req.Params)
		}
		if meta["io.modelcontextprotocol/protocolVersion"] != mcp.ProtocolVersionModern {
			t.Fatalf("protocol metadata = %+v", meta)
		}
		if _, ok := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any); !ok {
			t.Fatalf("client capabilities metadata = %+v", meta)
		}
		switch req.Method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[{"name":"search"}]}`)})
		case "tools/call":
			if got := r.Header.Get("Mcp-Name"); got != "search" {
				t.Fatalf("tool name header = %q", got)
			}
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	client, err := NewClientWithOptions(server.URL, server.Client(), mcp.ClientOptions{Mode: mcp.ProtocolModeModern})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(context.Background(), mcp.CallToolRequest{Name: "search"}); err != nil {
		t.Fatal(err)
	}
	beforeTerminate := requests.Load()
	if err := client.Terminate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != beforeTerminate {
		t.Fatal("modern terminate must be a transport no-op")
	}
	if client.SessionID() != "" {
		t.Fatalf("modern client must not expose a session id: %q", client.SessionID())
	}
}

func TestClientModernDoesNotRetryHTTPFailure(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		requests.Add(1)
		w.WriteHeader(nethttp.StatusNotFound)
	}))
	defer server.Close()
	client, err := NewClientWithOptions(server.URL, server.Client(), mcp.ClientOptions{Mode: mcp.ProtocolModeModern})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("expected modern HTTP failure")
	}
	if requests.Load() != 1 {
		t.Fatalf("modern client retried a failed request: %d requests", requests.Load())
	}
}
