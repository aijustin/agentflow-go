package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExecutorSendsAllowedRequestAndReturnsResponse(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != nethttp.MethodGet || r.URL.Path != "/status" || r.Header.Get("X-Trace") != "run-1" {
			t.Fatalf("unexpected request: method=%s path=%s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(nethttp.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, DefaultHeaders: map[string]string{"X-Trace": "run-1"}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"` + server.URL + `/status"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.StatusCode != nethttp.StatusAccepted || out.Body != `{"ok":true}` || out.Headers["Content-Type"] == "" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestExecutorRejectsHostOutsideAllowlist(t *testing.T) {
	executor, err := NewExecutor(Config{AllowedHosts: []string{"https://allowed.example"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"https://denied.example/status"}`)})
	if err == nil {
		t.Fatal("expected host allowlist error")
	}
}

func TestExecutorRejectsDisallowedMethodByDefault(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {}))
	defer server.Close()
	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "http.post", Input: json.RawMessage(`{"method":"POST","url":"` + server.URL + `/status"}`)})
	if err == nil {
		t.Fatal("expected method allowlist error")
	}
}

func TestExecutorRejectsRedirectToHostOutsideAllowlist(t *testing.T) {
	deniedHit := false
	denied := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		deniedHit = true
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer denied.Close()

	allowed := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, denied.URL+"/secret", nethttp.StatusFound)
	}))
	defer allowed.Close()

	executor, err := NewExecutor(Config{AllowedHosts: []string{allowed.URL}, Client: allowed.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"` + allowed.URL + `/start"}`)})
	if err == nil {
		t.Fatal("expected redirect to a disallowed host to be rejected")
	}
	if deniedHit {
		t.Fatal("request must not have reached the disallowed redirect target")
	}
}

func TestExecutorRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("too large"))
	}))
	defer server.Close()
	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, MaxResponseBytes: 3, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"` + server.URL + `"}`)})
	if err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestExecutorSendsIdempotencyKeyHeader(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if got := r.Header.Get("X-Idempotency-Key"); got != "run-1:call-1" {
			t.Fatalf("expected framework idempotency key header, got %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithIdempotencyKey(context.Background(), "run-1:call-1")
	// A caller-supplied header must not override the framework-injected key:
	// replay stability depends on the runtime's value reaching the upstream.
	input := `{"url":"` + server.URL + `","headers":{"X-Idempotency-Key":"caller-value"}}`
	if _, err := executor.Execute(ctx, core.ToolCall{Tool: "http.get", Input: json.RawMessage(input)}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorOmitsIdempotencyKeyHeaderWhenAbsent(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if got := r.Header.Get("X-Idempotency-Key"); got != "" {
			t.Fatalf("expected no idempotency key header without a context key, got %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), core.ToolCall{Tool: "http.get", Input: json.RawMessage(`{"url":"` + server.URL + `"}`)}); err != nil {
		t.Fatal(err)
	}
}
