package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestObservabilityWrappers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if agentflow.NewSlogEventSink(logger) == nil {
		t.Fatal("expected slog event sink")
	}
	if agentflow.NewVerboseSlogEventSink(logger) == nil {
		t.Fatal("expected verbose slog event sink")
	}
	if agentflow.NewSlogAuditSink(logger) == nil {
		t.Fatal("expected slog audit sink")
	}
	store := agentflow.NewInMemoryEventStore()
	hub := agentflow.NewEventHub()
	sink := agentflow.NewEventStoreSink(store, hub)
	if sink == nil {
		t.Fatal("expected event store sink")
	}
	recorder := agentflow.NewPrometheusRecorder()
	if recorder == nil || agentflow.PrometheusMetricsHandler(recorder) == nil {
		t.Fatal("expected prometheus recorder handler")
	}
}

func TestObservabilityHTTPHandlerWithStudioAdapter(t *testing.T) {
	scenario := core.Scenario{
		Name: "obs-studio",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	savePath := filepath.Join(t.TempDir(), "scenario.yaml")
	store := agentflow.NewInMemoryEventStore()
	handler, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
		Store:          store,
		Hub:            agentflow.NewEventHub(),
		Framework:      fw,
		StudioSavePath: savePath,
		TraceExploreURL: "https://trace.example/{trace_id}",
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := "obs-run"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	fork := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+runID+"/fork", bytes.NewBufferString(`{}`))
	handler.ServeHTTP(fork, req)
	if fork.Code != http.StatusOK {
		t.Fatalf("fork code=%d body=%s", fork.Code, fork.Body.String())
	}
	compare := httptest.NewRecorder()
	handler.ServeHTTP(compare, httptest.NewRequest(http.MethodGet, "/api/compare?run_a="+runID+"&run_b="+runID, nil))
	if compare.Code != http.StatusOK {
		t.Fatalf("compare code=%d body=%s", compare.Code, compare.Body.String())
	}
	save := httptest.NewRecorder()
	graph := fw.ExportScenarioGraph()
	body, _ := json.Marshal(graph)
	req = httptest.NewRequest(http.MethodPost, "/api/studio/save", bytes.NewReader(body))
	handler.ServeHTTP(save, req)
	if save.Code != http.StatusOK {
		t.Fatalf("save code=%d body=%s", save.Code, save.Body.String())
	}
	runStudio := httptest.NewRecorder()
	runPayload, _ := json.Marshal(map[string]any{
		"graph":  graph,
		"run_id": "obs-studio-run",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/studio/run", bytes.NewReader(runPayload))
	handler.ServeHTTP(runStudio, req)
	if runStudio.Code != http.StatusOK {
		t.Fatalf("studio run code=%d body=%s", runStudio.Code, runStudio.Body.String())
	}
	var runResult struct {
		Status runstate.RunStatus `json:"status"`
	}
	if err := json.Unmarshal(runStudio.Body.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	if runResult.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected studio run status: %+v", runResult)
	}
}
