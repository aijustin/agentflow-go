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
