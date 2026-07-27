package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// mrtrServer answers the first tools/call with a request for input and the
// retry with the real result, the way a stateless MCP server does under
// protocol 2026-07-28.
func mrtrServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Method != "tools/call" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(request.ID, 10) + `,"result":{}}`))
			return
		}
		calls = append(calls, request.Params)
		if len(calls) == 1 {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(request.ID, 10) + `,"result":{
				"resultType":"input_required",
				"inputRequests":{"which":{"method":"elicitation/create","params":{"message":"Which environment?"}}},
				"requestState":{"token":"s-1"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(request.ID, 10) + `,"result":{"content":[{"type":"text","text":"deployed to staging"}]}}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestCallToolCompletesMultiRoundTripOverHTTP(t *testing.T) {
	server, calls := mrtrServer(t)

	var asked mcp.ElicitRequest
	client, err := NewClientWithOptions(server.URL, server.Client(), mcp.ClientOptions{
		Mode: mcp.ProtocolModeModern,
		Elicitor: mcp.ElicitorFunc(func(_ context.Context, req mcp.ElicitRequest) (mcp.ElicitResult, error) {
			asked = req
			return mcp.ElicitResult{Action: mcp.ElicitAccept, Content: json.RawMessage(`{"env":"staging"}`)}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.CallTool(context.Background(), mcp.CallToolRequest{
		Name:      "deploy",
		Arguments: json.RawMessage(`{"service":"api"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "deployed to staging" {
		t.Fatalf("unexpected result %+v", result)
	}
	if asked.Message != "Which environment?" {
		t.Fatalf("elicitation did not reach the host: %+v", asked)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected two tools/call round trips, got %d", len(*calls))
	}

	retry := (*calls)[1]
	if retry["name"] != "deploy" {
		t.Fatalf("retry lost the tool name: %+v", retry)
	}
	if _, ok := retry["inputResponses"]; !ok {
		t.Fatalf("retry carried no answers: %+v", retry)
	}
	state, _ := json.Marshal(retry["requestState"])
	if string(state) != `{"token":"s-1"}` {
		t.Fatalf("requestState was not echoed: %s", state)
	}
}

// The client must tell the server it can be asked, or the server is forbidden
// from asking in the first place.
func TestModernRequestDeclaresElicitationCapability(t *testing.T) {
	var meta map[string]any
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			ID     int64 `json:"id"`
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Params.Meta != nil {
			meta = request.Params.Meta
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(request.ID, 10) + `,"result":{"content":[]}}`))
	}))
	defer server.Close()

	client, err := NewClientWithOptions(server.URL, server.Client(), mcp.ClientOptions{
		Mode: mcp.ProtocolModeModern,
		Elicitor: mcp.ElicitorFunc(func(context.Context, mcp.ElicitRequest) (mcp.ElicitResult, error) {
			return mcp.ElicitResult{Action: mcp.ElicitAccept}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(context.Background(), mcp.CallToolRequest{Name: "noop"}); err != nil {
		t.Fatal(err)
	}

	capsRaw, _ := json.Marshal(meta["io.modelcontextprotocol/clientCapabilities"])
	if !strings.Contains(string(capsRaw), "elicitation") {
		t.Fatalf("expected the elicitation capability advertised, got %s", capsRaw)
	}
}
