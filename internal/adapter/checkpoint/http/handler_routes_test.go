package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckpointHandlerNotConfiguredRoutes(t *testing.T) {
	handler := NewHandler(HandlerConfig{})
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/v1/runs/run-1/steps", http.StatusNotImplemented},
		{http.MethodPost, "/v1/runs/run-1/resume-from-step", http.StatusNotImplemented},
		{http.MethodGet, "/v1/runs/run-1/checkpoints", http.StatusNotImplemented},
		{http.MethodGet, "/v1/runs/run-1/checkpoints/1", http.StatusNotImplemented},
		{http.MethodPost, "/v1/runs/run-1/resume-from-checkpoint", http.StatusNotImplemented},
		{http.MethodPost, "/v1/runs/run-1/fork", http.StatusNotImplemented},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s expected %d, got %d body=%s", tc.method, tc.path, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestCheckpointHandlerMethodNotAllowedAndNotFound(t *testing.T) {
	handler := NewHandler(HandlerConfig{Steps: stubCheckpoint{steps: map[string]any{}}})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/runs/run-1/steps", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/other/run-1/steps", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for bad prefix, got %d", rec.Code)
	}
}

func TestCheckpointHandlerCheckpointsLimitValidation(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		History: stubCheckpoint{history: map[string]any{"checkpoints": []any{}}},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/checkpoints?limit=bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/checkpoints?limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCheckpointHandlerInvalidCheckpointVersion(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Checkpoints: stubCheckpoint{snapshot: map[string]any{"version": 1}},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/checkpoints/zero", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCheckpointHandlerResumeInvalidJSON(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Checkpoint: stubCheckpoint{result: map[string]any{}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/run-1/resume-from-step", strings.NewReader(`{`))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
