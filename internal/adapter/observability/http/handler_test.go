package http

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestHandlerServesDashboardRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	hub := obspkg.NewEventHub()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store, Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventLLMReturned, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 1, 0, time.UTC), Payload: json.RawMessage(`{"output":"done"}`)}); err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "AgentFlow 可观测性") || !strings.Contains(page.Body.String(), "id=\"langSelect\"") {
		t.Fatalf("dashboard page was not served: code=%d body=%q", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "const apiURL") || !strings.Contains(page.Body.String(), "capabilityBanner") {
		t.Fatalf("dashboard missing apiURL helper or capability banner")
	}

	runs := httptest.NewRecorder()
	handler.ServeHTTP(runs, httptest.NewRequest(http.MethodGet, "/api/runs?limit=5", nil))
	if runs.Code != http.StatusOK {
		t.Fatalf("list runs code=%d body=%s", runs.Code, runs.Body.String())
	}
	var runsBody struct {
		Runs []obspkg.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(runs.Body.Bytes(), &runsBody); err != nil {
		t.Fatal(err)
	}
	if len(runsBody.Runs) != 1 || runsBody.Runs[0].RunID != "run-1" || runsBody.Runs[0].EventCount != 2 {
		t.Fatalf("unexpected runs response: %+v", runsBody.Runs)
	}

	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events?after_sequence=1", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("list events code=%d body=%s", events.Code, events.Body.String())
	}
	var eventsBody struct {
		Events []obspkg.EventRecord `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &eventsBody); err != nil {
		t.Fatal(err)
	}
	if len(eventsBody.Events) != 1 || eventsBody.Events[0].Event.Type != core.EventLLMReturned {
		t.Fatalf("unexpected events response: %+v", eventsBody.Events)
	}
}

func TestHandlerStreamsRuntimeEvents(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	hub := obspkg.NewEventHub()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store, Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Append(ctx, core.Event{Type: core.EventToolCalled, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"tool":"search"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PublishEvent(ctx, record); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	requestCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/api/runs/run-1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected stream content type %q", resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && requestCtx.Err() != nil {
			t.Fatal("timed out waiting for ToolCalled stream event")
		}
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if strings.Contains(line, "ToolCalled") {
			cancel()
			return
		}
		if err == io.EOF {
			t.Fatal("stream closed before ToolCalled event")
		}
	}
}

func TestHandlerStudioEndpoints(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true,
		Store:       store,
		Graph:       graphStub{value: map[string]any{"name": "demo"}},
		Steps:       stepsStub{value: map[string]any{"run_id": "run-1", "steps": []any{}}},
		History:     checkpointStub{value: map[string]any{"run_id": "run-1", "checkpoints": []any{}}},
		Checkpoints: checkpointStub{value: map[string]any{"run_id": "run-1", "version": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := httptest.NewRecorder()
	handler.ServeHTTP(graph, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	if graph.Code != http.StatusOK {
		t.Fatalf("graph code=%d", graph.Code)
	}
	steps := httptest.NewRecorder()
	handler.ServeHTTP(steps, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/steps", nil))
	if steps.Code != http.StatusOK {
		t.Fatalf("steps code=%d", steps.Code)
	}
	checkpoints := httptest.NewRecorder()
	handler.ServeHTTP(checkpoints, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/checkpoints", nil))
	if checkpoints.Code != http.StatusOK {
		t.Fatalf("checkpoints code=%d", checkpoints.Code)
	}
	version := httptest.NewRecorder()
	handler.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/checkpoints/2", nil))
	if version.Code != http.StatusOK {
		t.Fatalf("checkpoint version code=%d", version.Code)
	}
}

func TestHandlerCompareResumeAndFork(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true,
		Store:   store,
		Resume:  resumeStub{value: map[string]any{"status": "completed"}},
		Restore: checkpointStub{value: map[string]any{"status": "completed"}},
		Compare: compareStub{value: map[string]any{"shared_steps": []any{}}},
		Thread:  threadStub{value: []any{map[string]any{"run_id": "run-1"}}},
		Fork:    forkStub{value: map[string]any{"run_id": "run-fork"}},
		Studio:  studioValidateStub{value: map[string]any{"valid": true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	compare := httptest.NewRecorder()
	handler.ServeHTTP(compare, httptest.NewRequest(http.MethodGet, "/api/compare?run_a=run-1&run_b=run-2", nil))
	if compare.Code != http.StatusOK {
		t.Fatalf("compare code=%d body=%s", compare.Code, compare.Body.String())
	}

	resume := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/resume-from-step", strings.NewReader(`{"node_id":"a"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(resume, req)
	if resume.Code != http.StatusOK {
		t.Fatalf("resume-from-step code=%d body=%s", resume.Code, resume.Body.String())
	}

	restore := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/runs/run-1/resume-from-checkpoint", strings.NewReader(`{"version":2}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(restore, req)
	if restore.Code != http.StatusOK {
		t.Fatalf("resume-from-checkpoint code=%d body=%s", restore.Code, restore.Body.String())
	}

	thread := httptest.NewRecorder()
	handler.ServeHTTP(thread, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/thread", nil))
	if thread.Code != http.StatusOK {
		t.Fatalf("thread code=%d body=%s", thread.Code, thread.Body.String())
	}

	fork := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/runs/run-1/fork", strings.NewReader(`{"version":1}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(fork, req)
	if fork.Code != http.StatusOK {
		t.Fatalf("fork code=%d body=%s", fork.Code, fork.Body.String())
	}

	validate := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/validate", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(validate, req)
	if validate.Code != http.StatusOK {
		t.Fatalf("studio validate code=%d body=%s", validate.Code, validate.Body.String())
	}
}

type resumeStub struct{ value any }

func (s resumeStub) ResumeFromStep(context.Context, string, string) (any, error) {
	return s.value, nil
}

type compareStub struct{ value any }

func (s compareStub) CompareRuns(context.Context, string, string) (any, error) {
	return s.value, nil
}

type threadStub struct{ value any }

func (s threadStub) ListRunThread(context.Context, string) (any, error) {
	return s.value, nil
}

type forkStub struct{ value any }

func (s forkStub) ForkRun(context.Context, string, int64) (any, error) {
	return s.value, nil
}

type studioValidateStub struct{ value any }

func (s studioValidateStub) ValidateStudioGraph(context.Context, any) (any, error) {
	return s.value, nil
}

type graphStub struct{ value any }

func (s graphStub) ExportScenarioGraph() any { return s.value }

type stepsStub struct{ value any }

func (s stepsStub) ListRunSteps(context.Context, string) (any, error) { return s.value, nil }

type checkpointStub struct{ value any }

func (s checkpointStub) ListRunCheckpoints(context.Context, string, int) (any, error) {
	return s.value, nil
}

func (s checkpointStub) GetRunCheckpoint(context.Context, string, int64) (any, error) {
	return s.value, nil
}

func (s checkpointStub) ResumeFromCheckpoint(context.Context, string, int64) (any, error) {
	return s.value, nil
}

func TestNewHandlerValidatesConfig(t *testing.T) {
	if _, err := NewHandler(Config{InsecureAllowNoAuth: true}); err == nil {
		t.Fatal("expected nil store error")
	}
}
