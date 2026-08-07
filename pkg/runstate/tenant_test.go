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

func TestScopeListFilterBindsAuthenticatedTenant(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	filter, err := ScopeListFilter(ctx, ListFilter{Status: RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if filter.TenantID != "tenant-a" {
		t.Fatalf("tenant_id = %q, want tenant-a", filter.TenantID)
	}
}

func TestScopeListFilterRejectsCrossTenantSelection(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	_, err := ScopeListFilter(ctx, ListFilter{TenantID: "tenant-b"})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

func TestScopeListFilterScopesPrincipallessCallers(t *testing.T) {
	// Strict is the default: an explicit tenant scope without a principal is
	// rejected (protected data), while the empty global maintenance view and
	// the permissive opt-out stay open.
	if _, err := ScopeListFilter(context.Background(), ListFilter{TenantID: "tenant-a"}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected tenant required, got %v", err)
	}
	if _, err := ScopeListFilter(context.Background(), ListFilter{}); err != nil {
		t.Fatalf("empty scope must stay open for maintenance callers, got %v", err)
	}
	filter, err := ScopeListFilter(ContextWithTenantPermissive(context.Background()), ListFilter{TenantID: "tenant-a"})
	if err != nil || filter.TenantID != "tenant-a" {
		t.Fatalf("permissive mode must allow explicit scopes, got %+v, %v", filter, err)
	}
}

type stubRepo struct {
	snapshot RunSnapshot
}

func (s stubRepo) Load(context.Context, string) (RunSnapshot, error) { return s.snapshot, nil }
func (s stubRepo) Save(context.Context, *RunSnapshot, int64) error   { return nil }
func (s stubRepo) Delete(context.Context, string) error              { return nil }
func (s stubRepo) List(context.Context, ListFilter) ([]RunSnapshot, error) {
	return nil, nil
}

func TestLoadAuthorizedEnforcesTenant(t *testing.T) {
	repo := stubRepo{snapshot: RunSnapshot{RunID: "run-1", TenantID: "tenant-b", Status: RunStatusRunning}}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	if _, err := LoadAuthorized(ctx, repo, "run-1"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
	ctx = identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-b"},
	})
	snapshot, err := LoadAuthorized(ctx, repo, "run-1")
	if err != nil || snapshot.RunID != "run-1" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}
