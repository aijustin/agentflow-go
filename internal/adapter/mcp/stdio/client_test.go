package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

func TestClientListsAndCallsToolsOverStdio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	result, err := client.CallTool(ctx, mcp.CallToolRequest{Name: "search", Arguments: json.RawMessage(`{"query":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientCallCancelledMidReadPoisonsClientInsteadOfHanging(t *testing.T) {
	client, err := NewClient(context.Background(), Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// "hang" never gets a reply from the helper process, so the call must
	// return once its own context expires rather than blocking forever.
	hangCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.call(hangCtx, "hang", nil, nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled call to return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call did not return after its context expired; it is hanging on the abandoned read")
	}

	// The abandoned read may still be sitting on the response meant for
	// the cancelled call; a fresh call must not silently consume it as its
	// own response, so the client should now be permanently poisoned.
	if err := client.call(context.Background(), "tools/list", nil, nil); err == nil {
		t.Fatal("expected subsequent calls on a poisoned client to fail")
	}
}

func TestClientHandlesLargeResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1", "AGENTFLOW_TEST_MCP_STDIO_BIG=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("large response must not break the client: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "big" || len(tools[0].Description) != 128*1024 {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestClientListToolsPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1", "AGENTFLOW_TEST_MCP_STDIO_PAGED=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "second" {
		t.Fatalf("expected both pages, got %+v", tools)
	}
}

func TestClientModernStdioSkipsHandshakeAndAddsMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env: []string{
			"AGENTFLOW_TEST_MCP_STDIO=1",
			"AGENTFLOW_TEST_MCP_STDIO_MODERN=1",
		},
		Options: mcp.ClientOptions{Mode: mcp.ProtocolModeModern},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("unexpected modern tools: %+v", tools)
	}
	result, err := client.CallTool(ctx, mcp.CallToolRequest{Name: "search"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected modern result: %+v", result)
	}
}

func TestStdioHelperProcess(t *testing.T) {
	if os.Getenv("AGENTFLOW_TEST_MCP_STDIO") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if req.ID == 0 {
			// Notifications (e.g. notifications/initialized) carry no id and
			// must not be answered, or the reply would be misread as the
			// response to a later request.
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			if os.Getenv("AGENTFLOW_TEST_MCP_STDIO_MODERN") == "1" {
				fmt.Fprintln(os.Stderr, "modern client sent initialize")
				os.Exit(2)
			}
			result = map[string]any{
				"protocolVersion": mcp.ProtocolVersionLegacy,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stub", "version": "0"},
			}
		case "tools/list":
			requireModernStdioMeta(req.Params)
			switch {
			case os.Getenv("AGENTFLOW_TEST_MCP_STDIO_BIG") == "1":
				// A single JSON-RPC line larger than bufio's 64KiB default;
				// the client must not die with "token too long".
				result = map[string]any{"tools": []map[string]any{{"name": "big", "description": strings.Repeat("x", 128*1024)}}}
			case os.Getenv("AGENTFLOW_TEST_MCP_STDIO_PAGED") == "1":
				var params map[string]any
				_ = json.Unmarshal(req.Params, &params)
				if params["cursor"] == "p2" {
					result = map[string]any{"tools": []map[string]any{{"name": "second"}}}
				} else {
					result = map[string]any{"tools": []map[string]any{{"name": "search"}}, "nextCursor": "p2"}
				}
			default:
				result = map[string]any{"tools": []map[string]any{{"name": "search", "description": "Search docs"}}}
			}
		case "tools/call":
			requireModernStdioMeta(req.Params)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		case "hang":
			// Deliberately never reply, to simulate a server that stalls
			// mid-request.
			continue
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func requireModernStdioMeta(params json.RawMessage) {
	if os.Getenv("AGENTFLOW_TEST_MCP_STDIO_MODERN") != "1" {
		return
	}
	var body map[string]any
	if err := json.Unmarshal(params, &body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	meta, ok := body["_meta"].(map[string]any)
	if !ok || meta["io.modelcontextprotocol/protocolVersion"] != mcp.ProtocolVersionModern {
		fmt.Fprintln(os.Stderr, "missing modern request metadata")
		os.Exit(2)
	}
	if _, ok := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any); !ok {
		fmt.Fprintln(os.Stderr, "missing modern client capabilities")
		os.Exit(2)
	}
}
