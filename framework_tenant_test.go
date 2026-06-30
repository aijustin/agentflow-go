package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkRunEnforcesTenantIsolation(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	tenantA := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "svc-a", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-a"}, Roles: []identity.Role{identity.RoleService},
	})
	result, err := fw.Run(tenantA, agentflow.RunRequest{RunID: "run-tenant-fw", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := fw.RunStateRepository().Load(tenantA, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", loaded.TenantID)
	}

	tenantB := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "svc-b", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-b"}, Roles: []identity.Role{identity.RoleService},
	})
	_, err = runstate.LoadAuthorized(tenantB, fw.RunStateRepository(), result.RunID)
	if !errors.Is(err, runstate.ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch on load, got %v", err)
	}
}

func TestFrameworkJobHandlerRejectsInvalidPrincipal(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.RunPayload{
		RunID:     "run-bad-principal",
		Agent:     "assistant",
		Prompt:    "hello",
		Principal: identity.Principal{ID: "user-1", Type: identity.PrincipalUser},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-bad", Type: asyncpkg.RunJobType, RunID: "run-bad-principal", Payload: payload})
	if err == nil || !strings.Contains(err.Error(), "invalid async job principal") {
		t.Fatalf("expected invalid principal error, got %v", err)
	}
}

func TestProductionHTTPHandlerMountsMetrics(t *testing.T) {
	recorder := agentflow.NewPrometheusRecorder()
	recorder.IncCounter(context.Background(), observability.MetricRuntimeEventsTotal)
	queue := agentflow.NewInMemoryJobQueue()
	handler, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
		Queue:          queue,
		Version:        "test",
		MetricsHandler: agentflow.PrometheusMetricsHandler(recorder),
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK {
		t.Fatalf("expected metrics ok, got %d", metrics.Code)
	}
	if !strings.Contains(metrics.Body.String(), "agentflow_runtime_events_total") {
		t.Fatalf("expected prometheus body, got %q", metrics.Body.String())
	}
}
