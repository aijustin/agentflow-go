package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		var result any
		switch req.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "search", "description": "Search docs"}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
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
