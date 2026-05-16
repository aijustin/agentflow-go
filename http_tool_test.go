package agentflow

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestHTTPToolRootConstructor(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	executor, err := NewHTTPToolExecutor(HTTPToolConfig{AllowedHosts: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"` + server.URL + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tool != "http.get" || len(result.Output) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
