package studiohttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	studiohttp "github.com/aijustin/agentflow-go/internal/adapter/studio/http"
)

type studioStub struct{}

func (studioStub) ValidateStudioGraph(_ context.Context, _ any) (any, error) {
	return map[string]any{"valid": true}, nil
}
func (studioStub) GenerateStudioBuilderCode(_ context.Context, _ any) (any, error) {
	return map[string]any{"language": "go", "code": "package main"}, nil
}
func (studioStub) GenerateStudioScenarioYAML(_ context.Context, _ any) (any, error) {
	return map[string]any{"language": "yaml", "code": "scenario:\n  name: demo"}, nil
}
func (studioStub) ImportStudioScenarioYAML(_ context.Context, _ []byte, _ any) (any, error) {
	return map[string]any{"scenario_name": "demo", "graph": map[string]any{"name": "demo"}}, nil
}
func (studioStub) RunStudioGraph(_ context.Context, _ any, _ any) (any, error) {
	return map[string]any{"run_id": "run-studio", "status": "completed"}, nil
}
func (studioStub) SaveStudioGraph(_ context.Context, _ any) (any, error) {
	return map[string]any{"path": "/tmp/scenario.yaml"}, nil
}

func TestHandlerStudioRoutes(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{
		Validate:   studioStub{},
		Codegen:    studioStub{},
		YAML:       studioStub{},
		ImportYAML: studioStub{},
		Run:        studioStub{},
		Save:       studioStub{},
	})
	cases := []struct {
		path string
		body string
	}{
		{path: "/v1/studio/validate", body: `{"name":"demo"}`},
		{path: "/v1/studio/codegen", body: `{"name":"demo"}`},
		{path: "/v1/studio/yaml", body: `{"name":"demo"}`},
		{path: "/v1/studio/import-yaml", body: `{"yaml":"scenario:\n  name: demo"}`},
		{path: "/v1/studio/save", body: `{"name":"demo"}`},
		{path: "/v1/studio/run", body: `{"graph":{"name":"demo"},"prompt":"hello"}`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlerStudioRouteErrors(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/studio/run", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/run", strings.NewReader(`{"prompt":"hello"}`)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerStudioRunRoute(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{
		Run: studioStub{},
	})
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"graph":{"name":"demo","workflow":{"nodes":[],"edges":[]}},"prompt":"hello"}`)
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/run", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["run_id"] != "run-studio" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandlerStudioImportAndSaveErrors(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{
		ImportYAML: studioStub{},
		Run:        studioStub{},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/import-yaml", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing yaml, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/run", strings.NewReader(`{"prompt":"hello"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing graph, got %d body=%s", rec.Code, rec.Body.String())
	}
}
