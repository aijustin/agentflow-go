package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestStampTenantFromContext(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	snapshot := &RunSnapshot{RunID: "run-1", Status: RunStatusRunning}
	StampTenant(ctx, snapshot)
	if snapshot.TenantID != "tenant-a" {
		t.Fatalf("tenant_id = %q, want tenant-a", snapshot.TenantID)
	}
}

func TestAuthorizeTenantRejectsCrossTenantAccess(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	err := AuthorizeTenant(ctx, RunSnapshot{RunID: "run-1", TenantID: "tenant-b", Status: RunStatusRunning})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

func TestAuthorizeTenantAllowsLegacySnapshots(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	if err := AuthorizeTenant(ctx, RunSnapshot{RunID: "run-1", Status: RunStatusRunning}); err != nil {
		t.Fatalf("legacy snapshot should remain accessible: %v", err)
	}
}
