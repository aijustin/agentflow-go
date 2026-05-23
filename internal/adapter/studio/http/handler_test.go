package studiohttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
func (studioStub) RunStudioGraph(_ context.Context, _ any, _ any) (any, error) {
	return map[string]any{"run_id": "run-studio", "status": "completed"}, nil
}
func (studioStub) SaveStudioGraph(_ context.Context, _ any) (any, error) {
	return map[string]any{"path": "/tmp/scenario.yaml"}, nil
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
