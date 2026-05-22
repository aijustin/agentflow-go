package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubCheckpoint struct {
	steps       any
	result      any
	history     any
	snapshot    any
	restore     any
	err         error
}

func (s stubCheckpoint) ListRunSteps(context.Context, string) (any, error) {
	return s.steps, s.err
}

func (s stubCheckpoint) ResumeFromStep(context.Context, string, string) (any, error) {
	return s.result, s.err
}

func (s stubCheckpoint) ListRunCheckpoints(context.Context, string, int) (any, error) {
	return s.history, s.err
}

func (s stubCheckpoint) GetRunCheckpoint(context.Context, string, int64) (any, error) {
	return s.snapshot, s.err
}

func (s stubCheckpoint) ResumeFromCheckpoint(context.Context, string, int64) (any, error) {
	return s.restore, s.err
}

func TestCheckpointHandlerStepsAndResume(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Steps:      stubCheckpoint{steps: map[string]any{"run_id": "run-1"}},
		Checkpoint: stubCheckpoint{result: map[string]any{"status": "completed"}},
	})

	steps := httptest.NewRecorder()
	handler.ServeHTTP(steps, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/steps", nil))
	if steps.Code != http.StatusOK {
		t.Fatalf("steps code=%d body=%s", steps.Code, steps.Body.String())
	}

	resume := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/run-1/resume-from-step", strings.NewReader(`{"node_id":"review"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(resume, req)
	if resume.Code != http.StatusOK {
		t.Fatalf("resume code=%d body=%s", resume.Code, resume.Body.String())
	}
}

func TestCheckpointHandlerHistoryAndRestore(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		History:     stubCheckpoint{history: map[string]any{"run_id": "run-1", "checkpoints": []any{}}},
		Checkpoints: stubCheckpoint{snapshot: map[string]any{"run_id": "run-1", "version": 2}},
		Restore:     stubCheckpoint{restore: map[string]any{"status": "completed"}},
	})

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/checkpoints", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("checkpoints code=%d body=%s", list.Code, list.Body.String())
	}

	load := httptest.NewRecorder()
	handler.ServeHTTP(load, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/checkpoints/2", nil))
	if load.Code != http.StatusOK {
		t.Fatalf("checkpoint version code=%d body=%s", load.Code, load.Body.String())
	}

	restore := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/run-1/resume-from-checkpoint", strings.NewReader(`{"version":2}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(restore, req)
	if restore.Code != http.StatusOK {
		t.Fatalf("restore code=%d body=%s", restore.Code, restore.Body.String())
	}
}
