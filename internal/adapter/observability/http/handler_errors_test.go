package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
)

type errStub struct{}

func (errStub) ListRunSteps(context.Context, string) (any, error) {
	return nil, errors.New("steps failed")
}
func (errStub) ResumeRunHITL(context.Context, string, core.Decision, json.RawMessage, bool) (any, error) {
	return nil, errors.New("hitl failed")
}
func (errStub) ResumeFromStep(context.Context, string, string) (any, error) {
	return nil, errors.New("resume failed")
}
func (errStub) ListRunCheckpoints(context.Context, string, int) (any, error) {
	return nil, errors.New("history failed")
}
func (errStub) GetRunCheckpoint(context.Context, string, int64) (any, error) {
	return nil, errors.New("checkpoint failed")
}
func (errStub) ResumeFromCheckpoint(context.Context, string, int64) (any, error) {
	return nil, errors.New("restore failed")
}
func (errStub) CompareRuns(context.Context, string, string) (any, error) {
	return nil, errors.New("compare failed")
}
func (errStub) ListRunThread(context.Context, string) (any, error) {
	return nil, errors.New("thread failed")
}
func (errStub) ForkRun(context.Context, string, int64) (any, error) {
	return nil, errors.New("fork failed")
}
func (errStub) ValidateStudioGraph(context.Context, any) (any, error) {
	return nil, errors.New("validate failed")
}
func (errStub) GenerateStudioBuilderCode(context.Context, any) (any, error) {
	return nil, errors.New("codegen failed")
}
func (errStub) GenerateStudioScenarioYAML(context.Context, any) (any, error) {
	return nil, errors.New("yaml failed")
}
func (errStub) ImportStudioScenarioYAML(context.Context, []byte, any) (any, error) {
	return nil, errors.New("import failed")
}
func (errStub) RunStudioGraph(context.Context, any, any) (any, error) {
	return nil, errors.New("run failed")
}
func (errStub) SaveStudioGraph(context.Context, any) (any, error) {
	return nil, errors.New("save failed")
}

func TestHandlerReturnsBadRequestForStubErrors(t *testing.T) {
	store := obsinmem.NewStore()
	stub := errStub{}
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true,
		Store:       store,
		Steps:       stub,
		HITLResume:  stub,
		Resume:      stub,
		History:     stub,
		Checkpoints: stub,
		Restore:     stub,
		Compare:     stub,
		Thread:      stub,
		Fork:        stub,
		Studio:      stub,
		Codegen:     stub,
		YAML:        stub,
		ImportYAML:  stub,
		RunStudio:   stub,
		StudioSave:  stub,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/runs/run-1/steps", ""},
		{http.MethodPost, "/api/runs/run-1/hitl/resume", `{"decision":"approve"}`},
		{http.MethodPost, "/api/runs/run-1/resume-from-step", `{"node_id":"a"}`},
		{http.MethodGet, "/api/runs/run-1/checkpoints", ""},
		{http.MethodGet, "/api/runs/run-1/checkpoints/2", ""},
		{http.MethodPost, "/api/runs/run-1/resume-from-checkpoint", `{"version":2}`},
		{http.MethodGet, "/api/compare?run_a=a&run_b=b", ""},
		{http.MethodGet, "/api/runs/run-1/thread", ""},
		{http.MethodPost, "/api/runs/run-1/fork", `{}`},
		{http.MethodPost, "/api/studio/validate", `{"name":"demo"}`},
		{http.MethodPost, "/api/studio/codegen", `{"name":"demo"}`},
		{http.MethodPost, "/api/studio/yaml", `{"name":"demo"}`},
		{http.MethodPost, "/api/studio/import-yaml", `{"yaml":"name: demo"}`},
		{http.MethodPost, "/api/studio/run", `{"graph":{"name":"demo"}}`},
		{http.MethodPost, "/api/studio/save", `{"name":"demo"}`},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		var req *http.Request
		if tc.body == "" {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		} else {
			req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
		}
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s expected 400, got %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerResumeEndpointsRejectInvalidBody(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true,
		Store:      store,
		Resume:     resumeStub{value: map[string]any{}},
		Restore:    checkpointStub{value: map[string]any{}},
		HITLResume: hitlStub{value: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/run-1/resume-from-step", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/run-1/resume-from-checkpoint", strings.NewReader(`{"version":0}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/run-1/hitl/resume", strings.NewReader(`{"decision":"bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
