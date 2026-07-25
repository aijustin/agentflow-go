package agentflow_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/httpx"
)

func TestNewCheckpointHTTPHandlerRequiresFramework(t *testing.T) {
	if _, err := httpx.NewCheckpointHTTPHandler(httpx.CheckpointHTTPHandlerConfig{}); err == nil {
		t.Fatal("expected framework required error")
	}
}

func TestNewCheckpointHTTPHandlerListsSteps(t *testing.T) {
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithCheckpointHistory(adapters.NewInMemoryCheckpointHistory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := "checkpoint-http-run"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	handler, err := httpx.NewCheckpointHTTPHandler(httpx.CheckpointHTTPHandlerConfig{
		Framework: fw, InsecureAllowNoAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID+"/steps", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected steps response: %d %s", rec.Code, rec.Body.String())
	}
}
