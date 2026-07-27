package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// An allowlisted host that redirects into the cloud metadata range must be
// stopped. The host allowlist alone does not catch this, and neither does a
// literal-IP check performed before the hostname is resolved.
func TestExecutorBlocksRedirectIntoMetadataRange(t *testing.T) {
	allowed := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", nethttp.StatusFound)
	}))
	defer allowed.Close()

	executor, err := NewExecutor(Config{
		AllowedHosts: []string{allowed.URL, "169.254.169.254"},
		Client:       allowed.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{
		Tool:  "http.get",
		Input: json.RawMessage(`{"url":"` + allowed.URL + `/start"}`),
	})
	if err == nil {
		t.Fatal("expected redirect into the metadata range to be blocked")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

// Even a directly requested metadata address must be refused when the operator
// has (mistakenly or maliciously) allowlisted it.
func TestExecutorBlocksAllowlistedMetadataHost(t *testing.T) {
	executor, err := NewExecutor(Config{AllowedHosts: []string{"169.254.169.254"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{
		Tool:  "http.get",
		Input: json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`),
	})
	if err == nil {
		t.Fatal("expected the metadata address to be blocked despite the allowlist")
	}
}

// With BlockLoopback set, a hostname that resolves to the loopback interface
// must be refused at dial time even though its name reveals nothing.
func TestExecutorBlockLoopbackRejectsHostnameResolvingToLoopback(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("local admin api"))
	}))
	defer server.Close()
	target := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	executor, err := NewExecutor(Config{
		AllowedHosts:  []string{target},
		BlockLoopback: true,
		Client:        server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{
		Tool:  "http.get",
		Input: json.RawMessage(`{"url":"` + target + `/"}`),
	})
	if err == nil {
		t.Fatal("expected loopback-resolving hostname to be blocked")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

// The default policy still permits loopback so local development and
// httptest-backed wiring keep working.
func TestExecutorAllowsLoopbackByDefault(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	executor, err := NewExecutor(Config{AllowedHosts: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{
		Tool:  "http.get",
		Input: json.RawMessage(`{"url":"` + server.URL + `/"}`),
	})
	if err != nil {
		t.Fatalf("expected loopback request to succeed by default, got %v", err)
	}
	if !strings.Contains(string(result.Output), "ok") {
		t.Fatalf("unexpected output %s", result.Output)
	}
}

// A client whose transport cannot be inspected cannot be guarded, so
// construction must fail rather than hand back an unprotected executor.
func TestNewExecutorFailsClosedOnUnguardableClient(t *testing.T) {
	client := &nethttp.Client{Transport: roundTripperFunc(func(*nethttp.Request) (*nethttp.Response, error) {
		return nil, nil
	})}
	_, err := NewExecutor(Config{AllowedHosts: []string{"example.com"}, Client: client})
	if err == nil {
		t.Fatal("expected construction to fail for an unguardable transport")
	}
}

type roundTripperFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripperFunc) RoundTrip(r *nethttp.Request) (*nethttp.Response, error) { return f(r) }
