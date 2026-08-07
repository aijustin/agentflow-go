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

// TestTenantStrictByDefault: tenant-strict is the default. A principal-less
// caller is denied exactly where data is tenant-protected (stamped
// snapshots); legacy unstamped snapshots stay open so single-tenant and
// internal paths keep working. ContextWithTenantPermissive opts back into
// fail-open.
func TestTenantStrictByDefault(t *testing.T) {
	stamped := RunSnapshot{RunID: "run-1", TenantID: "tenant-a"}
	unstamped := RunSnapshot{RunID: "run-2"}

	// Default (strict) + no principal: stamped snapshots fail closed.
	if err := AuthorizeTenant(context.Background(), stamped); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected ErrTenantRequired for principal-less access to stamped snapshot, got %v", err)
	}
	// Default + no principal: unstamped snapshots stay accessible.
	if err := AuthorizeTenant(context.Background(), unstamped); err != nil {
		t.Fatalf("unstamped snapshot must remain accessible, got %v", err)
	}
	// Permissive opt-out restores fail-open access to stamped snapshots.
	if err := AuthorizeTenant(ContextWithTenantPermissive(context.Background()), stamped); err != nil {
		t.Fatalf("permissive mode must allow principal-less access, got %v", err)
	}

	// Unstamped snapshot: any tenant principal is allowed (legacy data is
	// not tenant-protected).
	if err := AuthorizeTenant(tenantCtx("tenant-a"), unstamped); err != nil {
		t.Fatalf("unstamped snapshot must allow any principal, got %v", err)
	}
	// Matching tenant: allowed. Foreign tenant: mismatch.
	if err := AuthorizeTenant(tenantCtx("tenant-a"), stamped); err != nil {
		t.Fatalf("expected matching tenant to pass, got %v", err)
	}
	if err := AuthorizeTenant(tenantCtx("tenant-b"), stamped); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch for foreign tenant, got %v", err)
	}
}

// TestTenantStrictModeFromContextDefaultsStrict: strict is on unless the
// context is explicitly marked permissive.
func TestTenantStrictModeFromContextDefaultsStrict(t *testing.T) {
	if !TenantStrictModeFromContext(context.Background()) {
		t.Fatal("strict mode must be the default")
	}
	if TenantStrictModeFromContext(ContextWithTenantPermissive(context.Background())) {
		t.Fatal("permissive context must disable strict mode")
	}
}
