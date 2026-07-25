package http

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type stubResumer struct{ calls int }

func (s *stubResumer) ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error) {
	s.calls++
	return map[string]string{"status": "ok"}, nil
}

func (s *stubResumer) ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	s.calls++
	return map[string]string{"status": "ok"}, nil
}

func (s *stubResumer) ForkRun(ctx context.Context, runID string, version int64) (any, error) {
	s.calls++
	return map[string]string{"status": "ok"}, nil
}

func postJSON(t *testing.T, handler nethttp.Handler, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func getReq(t *testing.T, handler nethttp.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestWriteEndpointsDefaultDeny: without a Policy and without the explicit
// insecure opt-out, every mutating checkpoint endpoint must 403 with a
// structured auth_required code.
func TestWriteEndpointsDefaultDeny(t *testing.T) {
	stub := &stubResumer{}
	handler := NewHandler(HandlerConfig{Checkpoint: stub, Restore: stub, Fork: stub})
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/v1/runs/run-1/resume-from-step", `{"node_id":"n1"}`},
		{"/v1/runs/run-1/resume-from-checkpoint", `{"version":1}`},
		{"/v1/runs/run-1/fork", `{"version":1}`},
	} {
		rec := postJSON(t, handler, tc.path, tc.body)
		if rec.Code != nethttp.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		var body struct {
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: error body is not JSON: %v", tc.path, err)
		}
		if body.ErrorCode != "auth_required" {
			t.Fatalf("%s: expected auth_required code, got %q", tc.path, body.ErrorCode)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("denied requests must not reach the adapter, got %d calls", stub.calls)
	}
}

// TestWriteEndpointsInsecureOptOut: the explicit opt-out keeps the old
// open behavior for tests and proxied deployments.
func TestWriteEndpointsInsecureOptOut(t *testing.T) {
	stub := &stubResumer{}
	handler := NewHandler(HandlerConfig{Checkpoint: stub, Restore: stub, Fork: stub, InsecureAllowNoAuth: true})
	rec := postJSON(t, handler, "/v1/runs/run-1/resume-from-step", `{"node_id":"n1"}`)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with insecure opt-out, got %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("expected adapter call, got %d", stub.calls)
	}
}

type allowAllPolicy struct{ calls int }

func (p *allowAllPolicy) Authorize(_ context.Context, _ identity.Principal, _ security.Action, _ security.Resource) error {
	p.calls++
	return nil
}

// TestWriteEndpointsPolicyAuthorized: with a Policy, an authenticated
// principal passes and the write executes.
func TestWriteEndpointsPolicyAuthorized(t *testing.T) {
	stub := &stubResumer{}
	policy := &allowAllPolicy{}
	handler := NewHandler(HandlerConfig{Checkpoint: stub, Policy: policy})
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/runs/run-1/resume-from-step", bytes.NewBufferString(`{"node_id":"n1"}`))
	principal := identity.Principal{ID: "ops", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleApprover}}
	req = req.WithContext(identity.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 for authorized principal, got %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 || policy.calls != 1 {
		t.Fatalf("expected one adapter and one policy call, got %d/%d", stub.calls, policy.calls)
	}
}

// TestWriteEndpointsPolicyRequiresPrincipal: with a Policy, a request
// without a principal is unauthenticated (401), not forbidden-by-default.
func TestWriteEndpointsPolicyRequiresPrincipal(t *testing.T) {
	stub := &stubResumer{}
	handler := NewHandler(HandlerConfig{Checkpoint: stub, Policy: &allowAllPolicy{}})
	rec := postJSON(t, handler, "/v1/runs/run-1/resume-from-step", `{"node_id":"n1"}`)
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("expected 401 without principal, got %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("unauthenticated request must not reach the adapter, got %d calls", stub.calls)
	}
}

func TestReadEndpointsDefaultDenyWithoutPolicy(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Steps: stubSteps{result: map[string]string{"steps": "none"}},
	})
	rec := getReq(t, handler, "/v1/runs/run-1/steps")
	if rec.Code != nethttp.StatusForbidden {
		t.Fatalf("expected 403 for read endpoint, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReadEndpointsInsecureOptOut(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Steps:               stubSteps{result: map[string]string{"steps": "none"}},
		InsecureAllowNoAuth: true,
	})
	rec := getReq(t, handler, "/v1/runs/run-1/steps")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with insecure opt-out, got %d: %s", rec.Code, rec.Body.String())
	}
}

type stubSteps struct{ result any }

func (s stubSteps) ListRunSteps(context.Context, string) (any, error) { return s.result, nil }
