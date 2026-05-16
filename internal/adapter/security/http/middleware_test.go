package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestMiddlewareAuthorizesRequestWithPrincipal(t *testing.T) {
	principal := identity.Principal{ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleOperator}}
	policy := security.PolicyFunc(func(ctx context.Context, got identity.Principal, action security.Action, resource security.Resource) error {
		if got.ID != principal.ID || action != security.ActionRunSubmit || resource.ID != "run-1" {
			t.Fatalf("unexpected policy input principal=%+v action=%s resource=%+v", got, action, resource)
		}
		return nil
	})
	middleware, err := NewMiddleware(MiddlewareConfig{
		Policy: policy,
		Action: security.ActionRunSubmit,
		ResourceFunc: func(r *nethttp.Request) security.Resource {
			return security.Resource{Type: "run", ID: r.URL.Query().Get("run_id")}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		called = true
		w.WriteHeader(nethttp.StatusNoContent)
	})
	req := httptest.NewRequest(nethttp.MethodPost, "/api/run?run_id=run-1", nil)
	req = req.WithContext(identity.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	middleware(next).ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusNoContent || !called {
		t.Fatalf("expected authorized request to reach next, status=%d called=%v", rec.Code, called)
	}
}

func TestMiddlewareRejectsMissingPrincipalAndAudits(t *testing.T) {
	sink := &recordingAuditSink{}
	middleware, err := NewMiddleware(MiddlewareConfig{Policy: security.NewDefaultRolePolicy(), Action: security.ActionRunRead, Resource: security.Resource{Type: "run", ID: "run-1"}, Audit: sink})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	rec := httptest.NewRecorder()
	middleware(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true })).ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/runs/run-1", nil))
	if rec.Code != nethttp.StatusUnauthorized || called {
		t.Fatalf("expected 401 without next call, status=%d called=%v", rec.Code, called)
	}
	if len(sink.events) != 1 || sink.events[0].Type != audit.EventPolicyDenied || sink.events[0].Action != security.ActionRunRead {
		t.Fatalf("expected policy denied audit event, got %+v", sink.events)
	}
}

func TestMiddlewareRejectsDeniedPolicyAsForbiddenAndAudits(t *testing.T) {
	principal := identity.Principal{ID: "viewer-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}}
	sink := &recordingAuditSink{}
	middleware, err := NewMiddleware(MiddlewareConfig{Policy: security.NewDefaultRolePolicy(), Action: security.ActionRunCancel, Resource: security.Resource{Type: "run", ID: "run-1", TenantID: "tenant-1"}, Audit: sink})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/api/runs/run-1/cancel", nil)
	req = req.WithContext(identity.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	middleware(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { t.Fatal("next should not be called") })).ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if len(sink.events) != 1 || sink.events[0].Principal.ID != principal.ID || sink.events[0].Reason == "" {
		t.Fatalf("expected denied audit event with principal and reason, got %+v", sink.events)
	}
}

func TestNewMiddlewareRejectsInvalidConfig(t *testing.T) {
	if _, err := NewMiddleware(MiddlewareConfig{}); err == nil {
		t.Fatal("expected missing policy error")
	}
	if _, err := NewMiddleware(MiddlewareConfig{Policy: security.PolicyFunc(func(context.Context, identity.Principal, security.Action, security.Resource) error { return nil })}); err == nil {
		t.Fatal("expected missing action error")
	}
}

type recordingAuditSink struct {
	events []audit.Event
}

func (sink *recordingAuditSink) Record(ctx context.Context, event audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Reason == "force-error" {
		return errors.New("audit failed")
	}
	sink.events = append(sink.events, event)
	return nil
}
