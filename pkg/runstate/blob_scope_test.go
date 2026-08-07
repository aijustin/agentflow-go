package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestTenantScopedBlobIDsAreStableAndIsolated(t *testing.T) {
	tenantA := blobTenantContext("tenant-a")
	tenantB := blobTenantContext("tenant-b")
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
	tenantA := blobTenantContext("tenant-a")
	tenantB := blobTenantContext("tenant-b")
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
	// Legacy refs are unprotected data: readable with or without a principal.
	if err := AuthorizeBlobAccess(tenantA, legacy); err != nil {
		t.Fatalf("legacy blob must stay readable, got %v", err)
	}
	if err := AuthorizeBlobAccess(context.Background(), legacy); err != nil {
		t.Fatalf("legacy blob must stay readable without principal, got %v", err)
	}
	// Tenant-strict is the default: principal-less reads of tenant-scoped
	// refs fail closed; the permissive opt-out reopens them.
	if err := AuthorizeBlobAccess(context.Background(), ref); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected strict default to reject principal-less scoped read, got %v", err)
	}
	if err := AuthorizeBlobAccess(ContextWithTenantPermissive(context.Background()), ref); err != nil {
		t.Fatalf("permissive mode must allow principal-less scoped read, got %v", err)
	}
	// Principal-less writes create legacy unscoped refs (never rejected).
	unscoped, err := NewBlobRefForContext(context.Background(), []byte("x"))
	if err != nil {
		t.Fatalf("principal-less blob creation must stay open, got %v", err)
	}
	if unscoped.ID != unscoped.Sha256 {
		t.Fatalf("principal-less blob must use the legacy global ID, got %q", unscoped.ID)
	}
}

func TestFilterBlobRefsForTenantSkipsForeignAndLegacy(t *testing.T) {
	a, _ := NewBlobRefForContext(blobTenantContext("tenant-a"), []byte("a"))
	b, _ := NewBlobRefForContext(blobTenantContext("tenant-b"), []byte("b"))
	legacy := NewBlobRef("", []byte("legacy"))
	legacy.ID = legacy.Sha256
	got := FilterBlobRefsForTenant([]BlobRef{a, b, legacy}, "tenant-a")
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("expected only tenant-a ref, got %+v", got)
	}
}

func TestFilterBlobRefsForContextScopesPrincipallessStrictView(t *testing.T) {
	a, _ := NewBlobRefForContext(blobTenantContext("tenant-a"), []byte("a"))
	legacy := NewBlobRef("", []byte("legacy"))
	legacy.ID = legacy.Sha256
	refs := []BlobRef{a, legacy}

	// Strict default, no principal: only the unprotected legacy ref.
	got, err := FilterBlobRefsForContext(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != legacy.ID {
		t.Fatalf("expected only the legacy ref for principal-less strict view, got %+v", got)
	}
	// Permissive opt-out: global view.
	got, err = FilterBlobRefsForContext(ContextWithTenantPermissive(context.Background()), refs)
	if err != nil || len(got) != 2 {
		t.Fatalf("permissive mode must keep the global view, got %+v, %v", got, err)
	}
	// Authenticated tenant: own scoped refs plus legacy.
	got, err = FilterBlobRefsForContext(blobTenantContext("tenant-a"), refs)
	if err != nil || len(got) != 2 {
		t.Fatalf("tenant view must include own and legacy refs, got %+v, %v", got, err)
	}
}

func blobTenantContext(tenantID string) context.Context {
	return identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "user-" + tenantID,
		Type:  identity.PrincipalUser,
		Scope: identity.Scope{TenantID: tenantID},
	})
}
