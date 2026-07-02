package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestNewRetentionHTTPHandlerRequiresFramework(t *testing.T) {
	if _, err := agentflow.NewRetentionHTTPHandler(agentflow.RetentionHTTPHandlerConfig{}); err == nil {
		t.Fatal("expected framework required error")
	}
}

func TestNewRetentionHTTPHandlerPurgeRuns(t *testing.T) {
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := "retention-http-run"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewRetentionHTTPHandler(agentflow.RetentionHTTPHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"status":"completed","limit":10}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-runs", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge-runs code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Removed != 1 {
		t.Fatalf("expected 1 removed run, got %d", resp.Removed)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), runID)
	if err == nil {
		t.Fatalf("expected run to be purged, still loaded: %+v", snapshot)
	}
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found after purge, got %v", err)
	}
}
