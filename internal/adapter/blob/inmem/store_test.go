package inmem

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestStorePutGetCloneAndDigest(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	input := []byte("hello")

	ref, err := store.Put(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" || ref.Sha256 == "" || ref.Size != int64(len(input)) {
		t.Fatalf("unexpected ref: %+v", ref)
	}

	input[0] = 'H'
	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("store did not clone input, got %q", got)
	}

	got[0] = 'H'
	again, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "hello" {
		t.Fatalf("store did not clone output, got %q", again)
	}
}

func TestStoreGetMissing(t *testing.T) {
	store := NewStore()
	ref := runstate.NewBlobRef("", []byte("missing"))
	ref.ID = ref.Sha256
	_, err := store.Get(context.Background(), ref)
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreListAndDelete(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	ref, err := store.Put(ctx, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := store.List(ctx)
	if err != nil || len(refs) != 1 {
		t.Fatalf("unexpected list: %+v err=%v", refs, err)
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	refs, err = store.List(ctx)
	if err != nil || len(refs) != 0 {
		t.Fatalf("expected empty list after delete: %+v err=%v", refs, err)
	}
}

func TestStoreScopesBlobsByTenant(t *testing.T) {
	store := NewStore()
	tenantA := inmemBlobTenantContext("tenant-a")
	tenantB := inmemBlobTenantContext("tenant-b")
	refA, err := store.Put(tenantA, []byte("shared"))
	if err != nil {
		t.Fatal(err)
	}
	refB, err := store.Put(tenantB, []byte("shared"))
	if err != nil {
		t.Fatal(err)
	}
	if refA.ID == refB.ID {
		t.Fatal("expected tenant-scoped blob IDs")
	}
	if _, err := store.Get(tenantB, refA); !errors.Is(err, runstate.ErrTenantMismatch) {
		t.Fatalf("expected cross-tenant rejection, got %v", err)
	}
	refs, err := store.List(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != refA.ID {
		t.Fatalf("expected only tenant-a blob, got %+v", refs)
	}
}

func inmemBlobTenantContext(tenantID string) context.Context {
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "user-" + tenantID,
		Type:  identity.PrincipalUser,
		Scope: identity.Scope{TenantID: tenantID},
	})
	// Tenant-strict is the default; no wrapper needed.
	return ctx
}
