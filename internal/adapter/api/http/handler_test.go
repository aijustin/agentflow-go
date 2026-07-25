package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestHandlerMountsAsyncJobRoutes(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{
		Queue: queueinmem.NewQueue(), IDGenerator: func() string { return "job-1" }, InsecureAllowNoAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/jobs/events", strings.NewReader(`{"type":"ping"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected event job accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerExposesHealthAndReadiness(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) || !strings.Contains(rec.Body.String(), `"version":"test"`) {
			t.Fatalf("unexpected health body: %s", rec.Body.String())
		}
	}
}

func TestHandlerMountsAsyncRunsBehindAuthMiddleware(t *testing.T) {
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Auth") == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := identity.WithPrincipal(r.Context(), identity.Principal{
				ID:    "test-user",
				Type:  identity.PrincipalUser,
				Scope: identity.Scope{TenantID: "tenant-test"},
				Roles: []identity.Role{identity.RoleService},
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handler, err := NewHandler(HandlerConfig{
		Queue: queueinmem.NewQueue(), AuthMiddleware: authMiddleware,
		Policy: security.NewDefaultRolePolicy(), IDGenerator: func() string { return "run-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health should not require auth, got %d", health.Code)
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{"prompt":"hello"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected auth middleware to reject run submit, got %d", unauthorized.Code)
	}
	authorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("X-Test-Auth", "ok")
	handler.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("expected run submit accepted, got %d: %s", authorized.Code, authorized.Body.String())
	}
	unauthorizedJob := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedJob, httptest.NewRequest(http.MethodPost, "/v1/jobs/events", strings.NewReader(`{"type":"ping"}`)))
	if unauthorizedJob.Code != http.StatusUnauthorized {
		t.Fatalf("expected auth middleware to reject job submit, got %d", unauthorizedJob.Code)
	}
	authorizedJob := httptest.NewRecorder()
	jobReq := httptest.NewRequest(http.MethodPost, "/v1/jobs/events", strings.NewReader(`{"type":"ping","job_id":"job-event-auth"}`))
	jobReq.Header.Set("X-Test-Auth", "ok")
	handler.ServeHTTP(authorizedJob, jobReq)
	if authorizedJob.Code != http.StatusAccepted {
		t.Fatalf("expected authenticated job submit accepted, got %d: %s", authorizedJob.Code, authorizedJob.Body.String())
	}
}

func TestNewHandlerValidatesInputs(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{}); err == nil {
		t.Fatal("expected missing queue error")
	}
}

func TestHandlerOptionalRoutesAndHealthMethod(t *testing.T) {
	studio := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := NewHandler(HandlerConfig{
		Queue:            queueinmem.NewQueue(),
		EventsHandler:    http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		HITLHandler:      http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		StudioHandler:    studio,
		RetentionHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		MetricsHandler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/events", "/v1/hitl/resume", "/v1/studio/validate", "/v1/admin/retention/purge-blobs", "/metrics"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Fatalf("expected mounted route %s, got 404", path)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST healthz, got %d", rec.Code)
	}
}
