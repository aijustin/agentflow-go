package security

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestRolePolicyAuthorizesAllowedRole(t *testing.T) {
	policy := NewDefaultRolePolicy()
	principal := identity.Principal{ID: "operator-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleOperator}}
	err := policy.Authorize(context.Background(), principal, ActionRunSubmit, Resource{Type: "run", TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRolePolicyRejectsWrongRole(t *testing.T) {
	policy := NewDefaultRolePolicy()
	principal := identity.Principal{ID: "viewer-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}}
	err := policy.Authorize(context.Background(), principal, ActionRunCancel, Resource{Type: "run", TenantID: "tenant-1"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

func TestRolePolicyRejectsCrossTenantAccess(t *testing.T) {
	policy := NewDefaultRolePolicy()
	principal := identity.Principal{ID: "admin-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleAdmin}}
	err := policy.Authorize(context.Background(), principal, ActionRunRead, Resource{Type: "run", TenantID: "tenant-2"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected cross-tenant unauthorized, got %v", err)
	}
}

func TestPolicyAndAuthenticatorFuncWrappers(t *testing.T) {
	called := false
	policy := PolicyFunc(func(context.Context, identity.Principal, Action, Resource) error {
		called = true
		return nil
	})
	principal := identity.Principal{ID: "svc", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "t1"}}
	if err := policy.Authorize(context.Background(), principal, ActionRunSubmit, Resource{Type: "run"}); err != nil || !called {
		t.Fatalf("policy func: err=%v called=%v", err, called)
	}
	apiKey := APIKeyAuthenticatorFunc(func(_ context.Context, key string) (identity.Principal, bool, error) {
		return principal, key == "secret", nil
	})
	got, ok, err := apiKey.AuthenticateAPIKey(context.Background(), "secret")
	if err != nil || !ok || got.ID != "svc" {
		t.Fatalf("api key auth: %+v ok=%v err=%v", got, ok, err)
	}
	bearer := BearerAuthenticatorFunc(func(_ context.Context, token string) (identity.Principal, bool, error) {
		return principal, token == "tok", nil
	})
	got, ok, err = bearer.AuthenticateBearer(context.Background(), "tok")
	if err != nil || !ok || got.ID != "svc" {
		t.Fatalf("bearer auth: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestRolePolicyRejectsMissingPrincipal(t *testing.T) {
	err := NewDefaultRolePolicy().Authorize(context.Background(), identity.Principal{}, ActionRunRead, Resource{Type: "run"})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
}
