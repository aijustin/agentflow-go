package http

import (
	"bytes"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
)

// TestEndpointsDefaultDeny: without AuthMiddleware and without the explicit
// insecure opt-out, the dashboard and every API endpoint fail closed.
func TestEndpointsDefaultDeny(t *testing.T) {
	handler, err := NewHandler(Config{Store: obsinmem.NewStore()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{nethttp.MethodGet, "/", ""},
		{nethttp.MethodGet, "/api/runs", ""},
		{nethttp.MethodGet, "/api/runs/run-1/events", ""},
		{nethttp.MethodPost, "/api/runs/run-1/hitl/resume", `{"decision":"approve"}`},
		{nethttp.MethodPost, "/api/runs/run-1/resume-from-step", `{"node_id":"n1"}`},
		{nethttp.MethodPost, "/api/runs/run-1/resume-from-checkpoint", `{"version":1}`},
		{nethttp.MethodPost, "/api/runs/run-1/fork", `{"version":1}`},
		{nethttp.MethodPost, "/api/studio/run", `{"graph":{}}`},
		{nethttp.MethodPost, "/api/studio/save", `{}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != nethttp.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "auth_required") {
			t.Fatalf("%s: expected auth_required code, got %s", tc.path, rec.Body.String())
		}
	}
}

// TestEndpointsInsecureOptOut: the explicit opt-out restores the open local
// demo behavior for both reads and writes.
func TestEndpointsInsecureOptOut(t *testing.T) {
	handler, err := NewHandler(Config{
		Store:               obsinmem.NewStore(),
		InsecureAllowNoAuth: true,
		HITLResume:          hitlStub{value: map[string]string{"status": "ok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/api/runs/run-1/hitl/resume", bytes.NewBufferString(`{"decision":"approve"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with insecure opt-out, got %d: %s", rec.Code, rec.Body.String())
	}
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, httptest.NewRequest(nethttp.MethodGet, "/api/runs", nil))
	if readRec.Code != nethttp.StatusOK {
		t.Fatalf("expected read endpoint open with insecure opt-out, got %d: %s", readRec.Code, readRec.Body.String())
	}
}

// TestMutatingEndpointsOpenWithAuthMiddleware: when AuthMiddleware is set it
// owns the auth decision entirely and the default-deny guard is off.
func TestMutatingEndpointsOpenWithAuthMiddleware(t *testing.T) {
	var middlewareSawRequest bool
	handler, err := NewHandler(Config{
		Store:      obsinmem.NewStore(),
		HITLResume: hitlStub{value: map[string]string{"status": "ok"}},
		AuthMiddleware: func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				middlewareSawRequest = true
				next.ServeHTTP(w, r)
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/api/runs/run-1/hitl/resume", bytes.NewBufferString(`{"decision":"approve"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with AuthMiddleware, got %d: %s", rec.Code, rec.Body.String())
	}
	if !middlewareSawRequest {
		t.Fatal("expected the request to flow through AuthMiddleware")
	}
}

type countingLogger struct {
	mu    sync.Mutex
	warns int
}

func (l *countingLogger) Warn(context.Context, string, ...any) {
	l.mu.Lock()
	l.warns++
	l.mu.Unlock()
}

func (l *countingLogger) Error(context.Context, string, ...any) {}

// TestConstructionWarnsWithoutAuthMiddleware: building the handler without
// AuthMiddleware emits exactly one warning per construction.
func TestConstructionWarnsWithoutAuthMiddleware(t *testing.T) {
	logger := &countingLogger{}
	if _, err := NewHandler(Config{Store: obsinmem.NewStore(), Logger: logger}); err != nil {
		t.Fatal(err)
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.warns != 1 {
		t.Fatalf("expected one construction warning, got %d", logger.warns)
	}
}
