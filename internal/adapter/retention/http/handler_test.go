package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	retentionhttp "github.com/aijustin/agentflow-go/internal/adapter/retention/http"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type stubPurger struct {
	runsRemoved  int
	blobsRemoved int
	lastPolicy   retentionhttp.RetentionPolicy
}

func (s *stubPurger) PurgeRuns(context.Context, runstate.ListFilter) (int, error) {
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeExpired(context.Context, time.Duration) (int, error) {
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeWithPolicy(_ context.Context, policy retentionhttp.RetentionPolicy) (int, error) {
	s.lastPolicy = policy
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeOrphanBlobs(context.Context) (int, error) {
	return s.blobsRemoved, nil
}

func TestHandlerPurgePolicy(t *testing.T) {
	purger := &stubPurger{runsRemoved: 3}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"max_age":"1h","limit":10}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-policy", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if purger.lastPolicy.MaxAge != time.Hour || purger.lastPolicy.Limit != 10 {
		t.Fatalf("unexpected policy: %+v", purger.lastPolicy)
	}
	var resp struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Removed != 3 {
		t.Fatalf("expected removed=3, got %d", resp.Removed)
	}
}

func TestHandlerPurgeRuns(t *testing.T) {
	purger := &stubPurger{runsRemoved: 2}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"status":"completed","limit":5}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-runs", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerPurgeExpired(t *testing.T) {
	purger := &stubPurger{runsRemoved: 1}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"max_age":"30m"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-expired", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerPurgeExpiredRejectsInvalidDuration(t *testing.T) {
	purger := &stubPurger{}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"max_age":"not-a-duration"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-expired", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerPurgeBlobs(t *testing.T) {
	purger := &stubPurger{blobsRemoved: 4}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-blobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerNotFound(t *testing.T) {
	purger := &stubPurger{}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/retention/unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerPurgeRunsRequiresAuthorizationWhenPolicyConfigured(t *testing.T) {
	purger := &stubPurger{}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{
		Purger: purger,
		Policy: security.NewDefaultRolePolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-runs", bytes.NewBufferString(`{"limit":5}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerPurgePolicyRejectsInvalidDuration(t *testing.T) {
	purger := &stubPurger{}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"max_age":"bad"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-policy", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestNewHandlerRequiresPurger(t *testing.T) {
	if _, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{}); err == nil {
		t.Fatal("expected nil purger error")
	}
}

func TestHandlerAuthorizeAllowsAdminPrincipal(t *testing.T) {
	purger := &stubPurger{runsRemoved: 1}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{
		Purger: purger,
		Policy: security.NewDefaultRolePolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"limit":5}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-runs", body)
	req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{
		ID: "admin-1", Type: identity.PrincipalUser, Roles: []identity.Role{identity.RoleAdmin},
		Scope: identity.Scope{TenantID: "tenant-a"},
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerPurgeRunsRejectsMalformedJSON(t *testing.T) {
	purger := &stubPurger{}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-runs", bytes.NewBufferString(`{`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
