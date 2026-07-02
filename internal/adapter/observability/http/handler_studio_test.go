package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestHandlerStudioRoutesAndUIConfig(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{
		Store:          store,
		TraceExploreURL: "https://traces.example.com",
		Codegen:        studioStub{value: map[string]any{"code": "package main"}},
		YAML:           studioStub{value: map[string]any{"yaml": "name: demo"}},
		ImportYAML:     studioStub{value: map[string]any{"graph": map[string]any{"name": "demo"}}},
		RunStudio:      studioStub{value: map[string]any{"status": "completed"}},
		StudioSave:     studioStub{value: map[string]any{"saved": true}},
		HITLResume:     hitlStub{value: map[string]any{"status": "completed"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, httptest.NewRequest(http.MethodGet, "/api/ui-config", nil))
	if ui.Code != http.StatusOK || !strings.Contains(ui.Body.String(), "traces.example.com") {
		t.Fatalf("ui-config code=%d body=%s", ui.Code, ui.Body.String())
	}

	codegen := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/studio/codegen", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(codegen, req)
	if codegen.Code != http.StatusOK {
		t.Fatalf("codegen code=%d body=%s", codegen.Code, codegen.Body.String())
	}

	yaml := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/yaml", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(yaml, req)
	if yaml.Code != http.StatusOK {
		t.Fatalf("yaml code=%d body=%s", yaml.Code, yaml.Body.String())
	}

	importYAML := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/import-yaml", strings.NewReader(`{"yaml":"name: demo"}`))
	handler.ServeHTTP(importYAML, req)
	if importYAML.Code != http.StatusOK {
		t.Fatalf("import-yaml code=%d body=%s", importYAML.Code, importYAML.Body.String())
	}

	run := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/run", strings.NewReader(`{"graph":{"name":"demo"},"request":{"run_id":"run-1"}}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(run, req)
	if run.Code != http.StatusOK {
		t.Fatalf("studio run code=%d body=%s", run.Code, run.Body.String())
	}

	save := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/save", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(save, req)
	if save.Code != http.StatusOK {
		t.Fatalf("studio save code=%d body=%s", save.Code, save.Body.String())
	}

	hitl := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/runs/run-1/hitl/resume", strings.NewReader(`{"decision":"approve"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(hitl, req)
	if hitl.Code != http.StatusOK {
		t.Fatalf("hitl resume code=%d body=%s", hitl.Code, hitl.Body.String())
	}
}

func TestHandlerCompareRequiresQueryParams(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{Store: store, Compare: compareStub{value: map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/compare?run_a=only-a", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

type studioStub struct{ value any }

func (s studioStub) ValidateStudioGraph(_ context.Context, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) GenerateStudioBuilderCode(_ context.Context, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) GenerateStudioScenarioYAML(_ context.Context, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) ImportStudioScenarioYAML(_ context.Context, _ []byte, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) RunStudioGraph(_ context.Context, _, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) SaveStudioGraph(_ context.Context, _ any) (any, error) {
	return s.value, nil
}

type hitlStub struct{ value any }

func (s hitlStub) ResumeRunHITL(_ context.Context, _ string, _ core.Decision, _ json.RawMessage, _ bool) (any, error) {
	return s.value, nil
}
