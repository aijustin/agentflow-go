package http

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestHandlerSubmitsReadsAndCancelsRunJobs(t *testing.T) {
	queue := queueinmem.NewQueue()
	audits := auditinmem.NewSink(20)
	handler, err := NewHandler(HandlerConfig{Queue: queue, Policy: security.NewDefaultRolePolicy(), Audit: audits, IDGenerator: func() string { return "run-1" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleService}})

	submit := httptest.NewRecorder()
	handler.ServeHTTP(submit, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"agent":"assistant","prompt":"hello","context":{"k":"v"}}`)).WithContext(ctx))
	if submit.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected submit response: %d %s", submit.Code, submit.Body.String())
	}
	var submitted JobResponse
	if err := json.Unmarshal(submit.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.Job.ID != "run-1" || submitted.Job.RunID != "run-1" || submitted.Job.State != asyncpkg.JobQueued {
		t.Fatalf("unexpected submitted job: %+v", submitted.Job)
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(nethttp.MethodGet, "/v1/runs/run-1", nil).WithContext(ctx))
	if status.Code != nethttp.StatusOK {
		t.Fatalf("unexpected status response: %d %s", status.Code, status.Body.String())
	}

	cancel := httptest.NewRecorder()
	handler.ServeHTTP(cancel, httptest.NewRequest(nethttp.MethodPost, "/v1/runs/run-1/cancel", nil).WithContext(ctx))
	if cancel.Code != nethttp.StatusOK {
		t.Fatalf("unexpected cancel response: %d %s", cancel.Code, cancel.Body.String())
	}
	loaded, err := queue.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobCancelled {
		t.Fatalf("expected cancelled job, got %+v", loaded)
	}
	if !hasAuditEvent(audits.Events(), audit.EventRunSubmitted) || !hasAuditEvent(audits.Events(), audit.EventRunCancelled) {
		t.Fatalf("expected submit and cancel audit events, got %+v", audits.Events())
	}
}

func TestHandlerRequiresAuthorizationWhenPolicyConfigured(t *testing.T) {
	queue := queueinmem.NewQueue()
	audits := auditinmem.NewSink(20)
	handler, err := NewHandler(HandlerConfig{Queue: queue, Policy: security.NewDefaultRolePolicy(), Audit: audits, IDGenerator: func() string { return "run-1" }})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`)))
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", rec.Code)
	}
	if _, err := queue.Load(context.Background(), "run-1"); err == nil {
		t.Fatal("job should not be enqueued")
	}
	if !hasAuditEvent(audits.Events(), audit.EventPolicyDenied) {
		t.Fatalf("expected policy denied audit event, got %+v", audits.Events())
	}
}

func TestHandlerRejectsForbiddenCancel(t *testing.T) {
	queue := queueinmem.NewQueue()
	if _, err := queue.Enqueue(context.Background(), asyncpkg.Job{ID: "run-1", RunID: "run-1", Type: "run"}); err != nil {
		t.Fatal(err)
	}
	audits := auditinmem.NewSink(20)
	handler, err := NewHandler(HandlerConfig{Queue: queue, Policy: security.NewDefaultRolePolicy(), Audit: audits})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{ID: "viewer-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs/run-1/cancel", nil).WithContext(ctx))
	if rec.Code != nethttp.StatusForbidden {
		t.Fatalf("expected forbidden cancel, got %d", rec.Code)
	}
	loaded, err := queue.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobQueued {
		t.Fatalf("job should remain queued, got %+v", loaded)
	}
	if !hasAuditEvent(audits.Events(), audit.EventPolicyDenied) {
		t.Fatalf("expected policy denied audit event, got %+v", audits.Events())
	}
}

func TestHandlerReturnsNotFoundForMissingRun(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue()})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/v1/runs/missing", nil))
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("expected not found, got %d", rec.Code)
	}
}

func TestHandlerRejectsInvalidConfigAndBody(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{}); err == nil {
		t.Fatal("expected missing queue error")
	}
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue(), MaxBodyBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"too large"}`)))
	if rec.Code != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("expected body too large, got %d", rec.Code)
	}
}

func TestHandlerUsesConfiguredClockForGeneratedJobs(t *testing.T) {
	queue := queueinmem.NewQueue()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	handler, err := NewHandler(HandlerConfig{Queue: queue, IDGenerator: func() string { return "run-1" }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`)))
	if rec.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected submit response: %d %s", rec.Code, rec.Body.String())
	}
	loaded, err := queue.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.CreatedAt.Equal(now) || !loaded.UpdatedAt.Equal(now) || !loaded.AvailableAt.Equal(now) {
		t.Fatalf("expected configured timestamp, got %+v", loaded)
	}
}

func hasAuditEvent(events []audit.Event, typ audit.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
