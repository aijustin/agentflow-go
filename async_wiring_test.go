package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/builder"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestTierMemoryWrappers(t *testing.T) {
	hot := adapters.NewInMemoryTierHotStore()
	composite := adapters.NewCompositeTierStore(adapters.CompositeTierStoreConfig{Hot: hot})
	if composite == nil || hot == nil {
		t.Fatal("expected tier stores")
	}
	summarizer := adapters.NewLLMTierSummarizer(fakeGateway{content: "summary"}, "default")
	if summarizer == nil {
		t.Fatal("expected summarizer")
	}
}

func TestAsyncJobHandlerEventAndResume(t *testing.T) {
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "worker", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "t1"},
	})
	runPayload, _ := json.Marshal(asyncpkg.RunPayload{RunID: "job-run", Agent: "assistant", Prompt: "hi"})
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.RunJobType, Payload: runPayload}); err != nil {
		t.Fatal(err)
	}
	if adapters.NewInMemoryJobQueue() == nil {
		t.Fatal("expected in-memory job queue")
	}
}

func TestWebhookAndHumanHTTPHandlers(t *testing.T) {
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := httpx.NewWebhookHTTPHandler(httpx.WebhookHTTPHandlerConfig{Framework: fw}); err != nil {
		t.Fatal(err)
	}
	if httpx.NewHumanHTTPHandler(httpx.HumanHTTPHandlerConfig{Framework: fw}) == nil {
		t.Fatal("expected human handler")
	}
}

func TestAsyncJobHandlerEventAndResumeContinue(t *testing.T) {
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
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "done"}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eventPayload, _ := json.Marshal(asyncpkg.EventPayload{
		Type:    "ticket.created",
		Payload: json.RawMessage(`{"summary":"hello"}`),
	})
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.EventJobType, Payload: eventPayload}); err != nil {
		t.Fatalf("event job: %v", err)
	}
	hitlFW, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "done"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	hitlHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: hitlFW})
	if err != nil {
		t.Fatal(err)
	}
	result, err := hitlFW.Run(ctx, agentflow.RunRequest{RunID: "async-resume", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	resumePayload, _ := json.Marshal(asyncpkg.ResumeContinuePayload{
		Token:    result.Token,
		Decision: core.DecisionApprove,
	})
	if err := hitlHandler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.ResumeContinueJobType, Payload: resumePayload}); err != nil {
		t.Fatalf("resume continue job: %v", err)
	}
}

func TestHumanHTTPHandlerResumeWithoutContinue(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "done"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "human-resume", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	handler := httpx.NewHumanHTTPHandler(httpx.HumanHTTPHandlerConfig{Framework: fw})
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"token":"` + result.Token + `","decision":"approve"}`)
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/resume", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookHTTPHandlerAcceptsEvent(t *testing.T) {
	scenario := core.Scenario{
		Name: "webhook",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"support": {Name: "support", LLM: "default"},
		},
		Triggers: []core.Trigger{{
			Event: "ticket.created", Agent: "support", PromptPath: "summary",
		}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "handled"}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpx.NewWebhookHTTPHandler(httpx.WebhookHTTPHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"type":"ticket.created","payload":{"summary":"hello"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
