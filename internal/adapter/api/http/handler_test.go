package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
)

func TestHandlerExposesHealthAndReadiness(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) || !strings.Contains(rec.Body.String(), `"version":"test"`) {
			t.Fatalf("unexpected health body: %s", rec.Body.String())
		}
	}
}

func TestHandlerMountsAsyncRunsBehindAuthMiddleware(t *testing.T) {
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Auth") == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue(), AuthMiddleware: authMiddleware, IDGenerator: func() string { return "run-1" }})
	if err != nil {
		t.Fatal(err)
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health should not require auth, got %d", health.Code)
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{"prompt":"hello"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected auth middleware to reject run submit, got %d", unauthorized.Code)
	}
	authorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("X-Test-Auth", "ok")
	handler.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("expected run submit accepted, got %d: %s", authorized.Code, authorized.Body.String())
	}
}

func TestNewHandlerValidatesInputs(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{}); err == nil {
		t.Fatal("expected missing queue error")
	}
}
