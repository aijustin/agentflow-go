package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestTenantScopedBlobIDsAreStableAndIsolated(t *testing.T) {
	tenantA := blobTenantContext("tenant-a", false)
	tenantB := blobTenantContext("tenant-b", false)
	a1, err := NewBlobRefForContext(tenantA, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	a2, err := NewBlobRefForContext(tenantA, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBlobRefForContext(tenantB, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("same tenant and content must be stable: %q != %q", a1.ID, a2.ID)
	}
	if a1.ID == b.ID || a1.Sha256 != b.Sha256 {
		t.Fatalf("expected tenant-isolated IDs with the same digest: a=%+v b=%+v", a1, b)
	}
	if len(a1.ID) != 128 {
		t.Fatalf("expected 128-hex scoped id, got %q", a1.ID)
	}
}

func TestAuthorizeBlobAccessHonorsStrictTenantBoundary(t *testing.T) {
	tenantA := blobTenantContext("tenant-a", true)
	tenantB := blobTenantContext("tenant-b", true)
	ref, err := NewBlobRefForContext(tenantA, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeBlobAccess(tenantA, ref); err != nil {
		t.Fatalf("same-tenant access failed: %v", err)
	}
	if err := AuthorizeBlobAccess(tenantB, ref); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected cross-tenant rejection, got %v", err)
	}
	legacy := NewBlobRef("", []byte("legacy"))
	legacy.ID = legacy.Sha256
	if err := AuthorizeBlobAccess(tenantA, legacy); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected strict mode to reject legacy blob, got %v", err)
	}
	if _, err := NewBlobRefForContext(ContextWithTenantStrictMode(context.Background()), []byte("x")); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected strict mode to require tenant, got %v", err)
	}
}

func TestFilterBlobRefsForTenantSkipsForeignAndLegacy(t *testing.T) {
	a, _ := NewBlobRefForContext(blobTenantContext("tenant-a", false), []byte("a"))
	b, _ := NewBlobRefForContext(blobTenantContext("tenant-b", false), []byte("b"))
	legacy := NewBlobRef("", []byte("legacy"))
	legacy.ID = legacy.Sha256
	got := FilterBlobRefsForTenant([]BlobRef{a, b, legacy}, "tenant-a")
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("expected only tenant-a ref, got %+v", got)
	}
}

func blobTenantContext(tenantID string, strict bool) context.Context {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "user-" + tenantID,
		Type:  identity.PrincipalUser,
		Scope: identity.Scope{TenantID: tenantID},
	})
	if strict {
		ctx = ContextWithTenantStrictMode(ctx)
	}
	return ctx
}
