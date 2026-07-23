package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestObservabilitySinkWrappers(t *testing.T) {
	recorder := observability.NoopRecorder{}
	tracer := observability.NoopTracer{}
	next := adapters.NewSlogEventSink(nil)
	sink := adapters.NewObservabilityEventSink(recorder, tracer, next)
	if sink == nil {
		t.Fatal("expected observability event sink")
	}
	store := adapters.NewInMemoryEventStore()
	fanout := adapters.NewEventFanoutSink(adapters.NewEventStoreSink(store))
	if fanout == nil {
		t.Fatal("expected fanout sink")
	}
}

func TestKnowledgeRerankerWrappers(t *testing.T) {
	if adapters.NewScoreReranker() == nil {
		t.Fatal("expected score reranker")
	}
	if adapters.NewLLMReranker(fakeGateway{content: "rank"}, "default") == nil {
		t.Fatal("expected llm reranker")
	}
}

func TestInMemoryBlobStoreWrapper(t *testing.T) {
	store := adapters.NewInMemoryBlobStore()
	if store == nil {
		t.Fatal("expected blob store")
	}
	ref, err := store.Put(context.Background(), []byte("blob"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), ref)
	if err != nil || string(got) != "blob" {
		t.Fatalf("unexpected blob round trip: %q err=%v", got, err)
	}
}

func TestFrameworkStreamAutonomous(t *testing.T) {
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "hello"}, {Done: true}}}
	fw, err := agentflow.New(
		builder.MinimalAutonomous("assistant"),
		agentflow.WithLLMGateway(gateway),
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fw.Stream(context.Background(), agentflow.RunRequest{RunID: "stream-run", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for chunk := range ch {
		parts = append(parts, chunk.Content)
	}
	if len(parts) == 0 {
		t.Fatal("expected stream chunks")
	}
}

type streamGateway struct {
	chunks []llm.ChatChunk
}

func (g *streamGateway) StreamChat(_ context.Context, _ string, _ llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	out := make(chan llm.ChatChunk, len(g.chunks))
	for _, chunk := range g.chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

func (g *streamGateway) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *streamGateway) ChatWithTools(_ context.Context, _ string, _ llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{}, nil
}

func (g *streamGateway) Supports(_ string, cap llm.Capability) bool {
	switch cap {
	case llm.CapChat, llm.CapStream, llm.CapToolCall:
		return true
	default:
		return false
	}
}

func TestObservabilityHTTPHandlerStudioAdapterRoutes(t *testing.T) {
	scenario := core.Scenario{
		Name: "obs-full",
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "step", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"done":true}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithCheckpointHistory(adapters.NewInMemoryCheckpointHistory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	savePath := filepath.Join(t.TempDir(), "scenario.yaml")
	handler, err := httpx.NewObservabilityHTTPHandler(httpx.ObservabilityHTTPHandlerConfig{
		Store:          adapters.NewInMemoryEventStore(),
		Hub:            adapters.NewEventHub(),
		Framework:      fw,
		StudioSavePath: savePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := "obs-full-run"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	graph := httptest.NewRecorder()
	handler.ServeHTTP(graph, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	if graph.Code != http.StatusOK {
		t.Fatalf("graph code=%d", graph.Code)
	}
	steps := httptest.NewRecorder()
	handler.ServeHTTP(steps, httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/steps", nil))
	if steps.Code != http.StatusOK {
		t.Fatalf("steps code=%d body=%s", steps.Code, steps.Body.String())
	}
	checkpoints := httptest.NewRecorder()
	handler.ServeHTTP(checkpoints, httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/checkpoints", nil))
	if checkpoints.Code != http.StatusOK {
		t.Fatalf("checkpoints code=%d body=%s", checkpoints.Code, checkpoints.Body.String())
	}
	thread := httptest.NewRecorder()
	handler.ServeHTTP(thread, httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/thread", nil))
	if thread.Code != http.StatusOK {
		t.Fatalf("thread code=%d body=%s", thread.Code, thread.Body.String())
	}
	validate := httptest.NewRecorder()
	graphBody, _ := json.Marshal(fw.ExportScenarioGraph())
	req := httptest.NewRequest(http.MethodPost, "/api/studio/validate", bytes.NewReader(graphBody))
	handler.ServeHTTP(validate, req)
	if validate.Code != http.StatusOK {
		t.Fatalf("validate code=%d body=%s", validate.Code, validate.Body.String())
	}
	codegen := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/codegen", bytes.NewReader(graphBody))
	handler.ServeHTTP(codegen, req)
	if codegen.Code != http.StatusOK {
		t.Fatalf("codegen code=%d body=%s", codegen.Code, codegen.Body.String())
	}
	yaml := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/studio/yaml", bytes.NewReader(graphBody))
	handler.ServeHTTP(yaml, req)
	if yaml.Code != http.StatusOK {
		t.Fatalf("yaml code=%d body=%s", yaml.Code, yaml.Body.String())
	}
}
