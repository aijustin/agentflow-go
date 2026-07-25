package identity

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := Principal{ID: "svc-1", Type: PrincipalService, Scope: Scope{TenantID: "tenant-1"}, Roles: []Role{RoleService}}
	ctx := WithPrincipal(context.Background(), principal)
	got, err := RequirePrincipal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != principal.ID || got.Scope.TenantID != principal.Scope.TenantID {
		t.Fatalf("unexpected principal: %+v", got)
	}
	if !got.HasRole(RoleService) || got.HasRole(RoleAdmin) {
		t.Fatalf("unexpected roles: %+v", got.Roles)
	}
}

func TestRequirePrincipalMissing(t *testing.T) {
	_, err := RequirePrincipal(context.Background())
	if !errors.Is(err, ErrPrincipalMissing) {
		t.Fatalf("expected missing principal, got %v", err)
	}
}

func TestPrincipalValidateAndRoles(t *testing.T) {
	if err := (Principal{}).Validate(); !errors.Is(err, ErrPrincipalMissing) {
		t.Fatalf("expected validation error, got %v", err)
	}
	for _, principal := range []Principal{
		{ID: " ", Type: PrincipalUser, Scope: Scope{TenantID: "t1"}},
		{ID: "u1", Type: PrincipalUser, Scope: Scope{TenantID: " "}},
		{ID: " u1", Type: PrincipalUser, Scope: Scope{TenantID: "t1"}},
		{ID: "u1", Type: PrincipalUser, Scope: Scope{TenantID: "t1 "}},
	} {
		if err := principal.Validate(); !errors.Is(err, ErrPrincipalMissing) {
			t.Fatalf("expected non-canonical principal rejection for %+v, got %v", principal, err)
		}
	}
	principal := Principal{ID: "u1", Type: PrincipalUser, Scope: Scope{TenantID: "t1"}, Roles: []Role{RoleAdmin, RoleViewer}}
	if err := principal.Validate(); err != nil {
		t.Fatal(err)
	}
	if !principal.HasAnyRole(RoleAdmin, RoleOperator) || principal.HasAnyRole(RoleService) {
		t.Fatalf("unexpected HasAnyRole result for %+v", principal.Roles)
	}
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Fatal("expected missing principal in bare context")
	}
}
