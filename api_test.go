package agentflow_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestProductionHTTPHandlerMountsFrameworkRoutes(t *testing.T) {
	scenario := core.Scenario{
		Name: "ticket",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"support": {Name: "support", LLM: "default"},
		},
		Triggers: []core.Trigger{{
			Event:      "ticket.created",
			Agent:      "support",
			PromptPath: "summary",
		}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "ok"}))
	if err != nil {
		t.Fatal(err)
	}
	queue := agentflow.NewInMemoryJobQueue()
	handler, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
		Queue:     queue,
		Framework: fw,
		Version:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	event := httptest.NewRecorder()
	handler.ServeHTTP(event, httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{"type":"ticket.created","payload":{"summary":"hello"}}`)))
	if event.Code != http.StatusOK {
		t.Fatalf("expected sync event ok, got %d: %s", event.Code, event.Body.String())
	}

	asyncEvent := httptest.NewRecorder()
	handler.ServeHTTP(asyncEvent, httptest.NewRequest(http.MethodPost, "/v1/jobs/events", bytes.NewBufferString(`{"type":"ticket.created","payload":{"summary":"queued"}}`)))
	if asyncEvent.Code != http.StatusAccepted {
		t.Fatalf("expected async event accepted, got %d: %s", asyncEvent.Code, asyncEvent.Body.String())
	}
	var jobResp struct {
		Job struct {
			Type string `json:"type"`
		} `json:"job"`
	}
	if err := json.Unmarshal(asyncEvent.Body.Bytes(), &jobResp); err != nil {
		t.Fatal(err)
	}
	if jobResp.Job.Type != "event" {
		t.Fatalf("unexpected async job type: %+v", jobResp.Job)
	}

	hitl := httptest.NewRecorder()
	handler.ServeHTTP(hitl, httptest.NewRequest(http.MethodPost, "/v1/hitl/resume", bytes.NewBufferString(`{"token":"missing","decision":"approve","continue":true}`)))
	if hitl.Code == http.StatusNotFound {
		t.Fatal("expected /v1/hitl/resume to be mounted")
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("expected health ok, got %d", health.Code)
	}
}
