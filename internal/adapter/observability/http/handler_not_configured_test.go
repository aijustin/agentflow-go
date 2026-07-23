package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
)

func TestHandlerNotConfiguredEndpoints(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/graph", http.StatusNotImplemented},
		{http.MethodGet, "/api/compare?run_a=a&run_b=b", http.StatusNotImplemented},
		{http.MethodGet, "/api/runs/run-1/steps", http.StatusNotImplemented},
		{http.MethodPost, "/api/runs/run-1/resume-from-step", http.StatusNotImplemented},
		{http.MethodGet, "/api/runs/run-1/checkpoints", http.StatusNotImplemented},
		{http.MethodGet, "/api/runs/run-1/checkpoints/1", http.StatusNotImplemented},
		{http.MethodPost, "/api/runs/run-1/resume-from-checkpoint", http.StatusNotImplemented},
		{http.MethodPost, "/api/runs/run-1/hitl/resume", http.StatusNotImplemented},
		{http.MethodGet, "/api/runs/run-1/thread", http.StatusNotImplemented},
		{http.MethodPost, "/api/runs/run-1/fork", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/validate", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/codegen", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/yaml", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/import-yaml", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/run", http.StatusNotImplemented},
		{http.MethodPost, "/api/studio/save", http.StatusNotImplemented},
		{http.MethodPost, "/api/runs/run-1/resume-from-step", http.StatusNotImplemented},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s expected %d, got %d body=%s", tc.method, tc.path, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store, Graph: graphStub{value: map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/graph", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlerDashboardNotFoundSubpath(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
