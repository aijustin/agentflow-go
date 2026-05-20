package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkHandleEventUsesTriggers(t *testing.T) {
	scenario := core.Scenario{
		Name: "ticket",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"support": {Name: "support", LLM: "default"},
		},
		Triggers: []core.Trigger{{
			Event:       "ticket.created",
			Agent:       "support",
			PromptPath:  "body.summary",
			ContextPath: "body",
			RunIDPath:   "body.ticket_id",
		}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "reply"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.HandleEvent(context.Background(), agentflow.IncomingEvent{
		Type:    "ticket.created",
		Payload: json.RawMessage(`{"body":{"ticket_id":"T-9","summary":"Need help"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "T-9" || result.Output != "reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWebhookHTTPHandlerDispatchesEvent(t *testing.T) {
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
	handler, err := agentflow.NewWebhookHTTPHandler(agentflow.WebhookHTTPHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{"type":"ticket.created","payload":{"summary":"hello"}}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Output string             `json:"output"`
		Status runstate.RunStatus `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Output != "ok" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected body: %+v", result)
	}
}
