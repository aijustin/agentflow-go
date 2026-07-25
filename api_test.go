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
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/httpx"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
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
	queue := adapters.NewInMemoryJobQueue()
	handler, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue:               queue,
		Framework:           fw,
		Version:             "test",
		StudioSavePath:      t.TempDir() + "/scenario.yaml",
		InsecureAllowNoAuth: true,
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

	checkpoint := httptest.NewRecorder()
	// Seed the run first: a missing run now correctly reports 404, which is
	// indistinguishable from an unmounted route.
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-1", Agent: "support", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(checkpoint, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/steps", nil))
	if checkpoint.Code == http.StatusNotFound {
		t.Fatal("expected checkpoint routes to be mounted")
	}
}

func TestProductionHTTPHandlerRequiresSecurityWiring(t *testing.T) {
	queue := adapters.NewInMemoryJobQueue()
	if _, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{Queue: queue}); err == nil {
		t.Fatal("expected missing AuthMiddleware error")
	}
	auth := func(next http.Handler) http.Handler { return next }
	if _, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue: queue, AuthMiddleware: auth,
	}); err == nil {
		t.Fatal("expected missing Policy error")
	}
	if _, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue: queue, AuthMiddleware: auth, Policy: security.NewDefaultRolePolicy(),
	}); err != nil {
		t.Fatalf("expected secure wiring to succeed: %v", err)
	}
}

func TestProductionHTTPHandlerWiresWebhookSignatureVerifier(t *testing.T) {
	scenario := core.Scenario{
		Name: "signed-event",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"support": {Name: "support", LLM: "default"},
		},
		Triggers:      []core.Trigger{{Event: "ticket.created", Agent: "support", PromptPath: "summary"}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "ok"}))
	if err != nil {
		t.Fatal(err)
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := identity.Principal{
				ID: "webhook", Type: identity.PrincipalService,
				Scope: identity.Scope{TenantID: "tenant-a"}, Roles: []identity.Role{identity.RoleService},
			}
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), principal)))
		})
	}
	handler, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
		Queue:          adapters.NewInMemoryJobQueue(),
		Framework:      fw,
		Policy:         security.NewDefaultRolePolicy(),
		AuthMiddleware: auth,
		VerifyWebhookSignature: func(r *http.Request, _ []byte) error {
			if r.Header.Get("X-Signature") != "valid" {
				return errors.New("bad signature")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"type":"ticket.created","payload":{"summary":"hello"}}`
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body)))
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid signature rejection, got %d: %s", rejected.Code, rejected.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	req.Header.Set("X-Signature", "valid")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, req)
	if accepted.Code != http.StatusOK {
		t.Fatalf("expected valid signature, got %d: %s", accepted.Code, accepted.Body.String())
	}
}
