package file

import (
	"bytes"
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestStorePersistsBlobs(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(ctx, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("unexpected blob %q", got)
	}
}

func TestStoreListAndDelete(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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

func TestStoreGetRejectsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(ctx, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	ref.Sha256 = "deadbeef"
	if _, err := store.Get(ctx, ref); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestStoreGetMissingBlob(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, runstate.BlobRef{ID: "missing", Sha256: "missing"}); err == nil {
		t.Fatal("expected missing blob error")
	}
}
