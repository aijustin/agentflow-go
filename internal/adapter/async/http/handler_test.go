package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestHandlerSubmitsEventAndResumeContinueJobs(t *testing.T) {
	queue := queueinmem.NewQueue()
	audits := auditinmem.NewSink(20)
	handler, err := NewHandler(HandlerConfig{Queue: queue, Policy: security.NewDefaultRolePolicy(), Audit: audits, IDGenerator: func() string { return "job-1" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleService}})
	approverCtx := identity.WithPrincipal(context.Background(), identity.Principal{ID: "approver-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleApprover}})

	eventSubmit := httptest.NewRecorder()
	handler.ServeHTTP(eventSubmit, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/events", bytes.NewBufferString(`{"type":"ticket.created","job_id":"job-event","payload":{"id":"t-1"}}`)).WithContext(ctx))
	if eventSubmit.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected event submit response: %d %s", eventSubmit.Code, eventSubmit.Body.String())
	}
	var eventJob JobResponse
	if err := json.Unmarshal(eventSubmit.Body.Bytes(), &eventJob); err != nil {
		t.Fatal(err)
	}
	if eventJob.Job.Type != asyncpkg.EventJobType || eventJob.Job.State != asyncpkg.JobQueued {
		t.Fatalf("unexpected event job: %+v", eventJob.Job)
	}

	resumeSubmit := httptest.NewRecorder()
	handler.ServeHTTP(resumeSubmit, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/hitl/resume", bytes.NewBufferString(`{"token":"tok-1","decision":"approve","job_id":"job-resume"}`)).WithContext(approverCtx))
	if resumeSubmit.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected resume submit response: %d %s", resumeSubmit.Code, resumeSubmit.Body.String())
	}
	var resumeJob JobResponse
	if err := json.Unmarshal(resumeSubmit.Body.Bytes(), &resumeJob); err != nil {
		t.Fatal(err)
	}
	if resumeJob.Job.Type != asyncpkg.ResumeContinueJobType {
		t.Fatalf("unexpected resume job type: %+v", resumeJob.Job)
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(nethttp.MethodGet, "/v1/jobs/job-event", nil).WithContext(ctx))
	if status.Code != nethttp.StatusOK {
		t.Fatalf("unexpected job status response: %d %s", status.Code, status.Body.String())
	}
}

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

func TestHandlerCancelUpdatesRunState(t *testing.T) {
	queue := queueinmem.NewQueue()
	runs := runstateinmem.NewRepository()
	if _, err := queue.Enqueue(context.Background(), asyncpkg.Job{ID: "run-1", RunID: "run-1", Type: asyncpkg.RunJobType}); err != nil {
		t.Fatal(err)
	}
	if err := runs.Save(context.Background(), &runstate.RunSnapshot{
		RunID:        "run-1",
		ScenarioName: "scenario",
		Status:       runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerConfig{
		Queue:    queue,
		RunState: runs,
		Policy:   security.NewDefaultRolePolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "svc-1",
		Type:  identity.PrincipalService,
		Scope: identity.Scope{TenantID: "tenant-1"},
		Roles: []identity.Role{identity.RoleService},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs/run-1/cancel", nil).WithContext(ctx))
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("unexpected cancel response: %d %s", rec.Code, rec.Body.String())
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled run snapshot, got %+v", snapshot)
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
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListJobsAndRequeue(t *testing.T) {
	queue := queueinmem.NewQueue()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-dead", Type: asyncpkg.RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease: ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerConfig{Queue: queue})
	if err != nil {
		t.Fatal(err)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(nethttp.MethodGet, "/v1/jobs?state=dead_letter", nil))
	if list.Code != nethttp.StatusOK {
		t.Fatalf("list jobs code=%d body=%s", list.Code, list.Body.String())
	}
	requeue := httptest.NewRecorder()
	handler.ServeHTTP(requeue, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/job-dead/requeue", nil))
	if requeue.Code != nethttp.StatusOK {
		t.Fatalf("requeue code=%d body=%s", requeue.Code, requeue.Body.String())
	}
}

func TestHandlerRejectsInvalidEventAndResumeBodies(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue()})
	if err != nil {
		t.Fatal(err)
	}
	event := httptest.NewRecorder()
	handler.ServeHTTP(event, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/events", bytes.NewBufferString(`{"payload":{}}`)))
	if event.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected 400 for missing event type, got %d", event.Code)
	}
	resume := httptest.NewRecorder()
	handler.ServeHTTP(resume, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/hitl/resume", bytes.NewBufferString(`{"token":"tok"}`)))
	if resume.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected 400 for missing decision, got %d", resume.Code)
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

func TestHandlerDefaultGenerateRunID(t *testing.T) {
	queue := queueinmem.NewQueue()
	handler, err := NewHandler(HandlerConfig{Queue: queue})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`)))
	if rec.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected submit response: %d %s", rec.Code, rec.Body.String())
	}
	var resp JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Job.ID == "" {
		t.Fatal("expected generated run id")
	}
	if _, err := queue.Load(context.Background(), resp.Job.ID); err != nil {
		t.Fatalf("expected enqueued job: %v", err)
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

func TestHandlerSubmitRunWithoutPolicyUsesContextPrincipal(t *testing.T) {
	queue := queueinmem.NewQueue()
	handler, err := NewHandler(HandlerConfig{Queue: queue, IDGenerator: func() string { return "run-open" }})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`)))
	if rec.Code != nethttp.StatusAccepted {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCancelCompletedRunStateIsNoOp(t *testing.T) {
	queue := queueinmem.NewQueue()
	runs := runstateinmem.NewRepository()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "run-done", RunID: "run-done", Type: asyncpkg.RunJobType}); err != nil {
		t.Fatal(err)
	}
	if err := runs.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-done", ScenarioName: "scenario", Status: runstate.RunStatusCompleted,
	}, 0); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerConfig{Queue: queue, RunState: runs})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/runs/run-done/cancel", nil))
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("unexpected cancel response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCancelJobRoute(t *testing.T) {
	queue := queueinmem.NewQueue()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.EventJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerConfig{Queue: queue})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/job-1/cancel", nil))
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("unexpected cancel response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerNotFoundAndMethodNotAllowed(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue()})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/v1/runs", nil))
	if rec.Code != nethttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/v1/unknown", nil))
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListJobsRequiresAdminQueue(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: stubQueueWithoutAdmin{Queue: queueinmem.NewQueue()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/v1/jobs", nil))
	if rec.Code != nethttp.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestHandlerRequeueRequiresAdminQueue(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: stubQueueWithoutAdmin{Queue: queueinmem.NewQueue()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/job-1/requeue", nil))
	if rec.Code != nethttp.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestHandlerSubmitResumeRejectsInvalidDecision(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queue: queueinmem.NewQueue()})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/jobs/hitl/resume", bytes.NewBufferString(`{"token":"tok","decision":"bogus"}`)))
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected 400 for invalid decision, got %d", rec.Code)
	}
}

type stubQueueWithoutAdmin struct {
	asyncpkg.Queue
}

func hasAuditEvent(events []audit.Event, typ audit.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
