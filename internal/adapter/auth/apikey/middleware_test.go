package apikey

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestMiddlewareAuthenticatesBearerToken(t *testing.T) {
	auth, err := NewStaticAuthenticator(map[string]identity.Principal{
		"secret": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewMiddleware(MiddlewareConfig{Authenticator: auth})
	if err != nil {
		t.Fatal(err)
	}
	next := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := identity.RequirePrincipal(r.Context())
		if err != nil {
			t.Fatal(err)
		}
		if principal.ID != "svc-1" {
			t.Fatalf("unexpected principal: %+v", principal)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected no content, got %d", rec.Code)
	}
}

func TestMiddlewareAuthenticatesCustomHeader(t *testing.T) {
	auth, err := NewStaticAuthenticator(map[string]identity.Principal{
		"secret": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewMiddleware(MiddlewareConfig{Authenticator: auth, HeaderName: "X-Test-Key"})
	if err != nil {
		t.Fatal(err)
	}
	next := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Test-Key", "secret")
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted, got %d", rec.Code)
	}
}

func TestMiddlewareRejectsMissingKey(t *testing.T) {
	auth, err := NewStaticAuthenticator(map[string]identity.Principal{
		"secret": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewMiddleware(MiddlewareConfig{Authenticator: auth})
	if err != nil {
		t.Fatal(err)
	}
	next := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("next should not be called") }))
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", rec.Code)
	}
}
