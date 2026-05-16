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
