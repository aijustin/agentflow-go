package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func tenantCtx(tenantID string) context.Context {
	return identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "user-1",
		Type:  identity.PrincipalUser,
		Scope: identity.Scope{TenantID: tenantID},
	})
}

// TestTenantStrictModeRejectsUnauthenticatedAndUnstamped: in strict mode a
// missing principal and a legacy unstamped snapshot both fail closed, while
// the default mode keeps its permissive behavior.
func TestTenantStrictModeRejectsUnauthenticatedAndUnstamped(t *testing.T) {
	strict := ContextWithTenantStrictMode(context.Background())
	stamped := RunSnapshot{RunID: "run-1", TenantID: "tenant-a"}
	unstamped := RunSnapshot{RunID: "run-2"}

	// Strict + no principal: reject regardless of snapshot.
	if err := AuthorizeTenant(strict, stamped); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected ErrTenantRequired without principal, got %v", err)
	}
	if err := AuthorizeTenant(strict, unstamped); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected ErrTenantRequired without principal, got %v", err)
	}
	// Default + no principal: still open (backward compatibility).
	if err := AuthorizeTenant(context.Background(), stamped); err != nil {
		t.Fatalf("default mode must allow principal-less access, got %v", err)
	}

	// Strict + unstamped snapshot: any tenant principal is rejected.
	if err := AuthorizeTenant(ContextWithTenantStrictMode(tenantCtx("tenant-a")), unstamped); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch for unstamped snapshot, got %v", err)
	}
	// Default + unstamped snapshot: still open.
	if err := AuthorizeTenant(tenantCtx("tenant-a"), unstamped); err != nil {
		t.Fatalf("default mode must allow unstamped snapshots, got %v", err)
	}

	// Strict + matching tenant: allowed.
	if err := AuthorizeTenant(ContextWithTenantStrictMode(tenantCtx("tenant-a")), stamped); err != nil {
		t.Fatalf("expected matching tenant to pass in strict mode, got %v", err)
	}
	// Strict + wrong tenant: mismatch (unchanged behavior).
	if err := AuthorizeTenant(ContextWithTenantStrictMode(tenantCtx("tenant-b")), stamped); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch for foreign tenant, got %v", err)
	}
}
