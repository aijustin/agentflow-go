package inmem

import (
	"context"
	"errors"
	"testing"

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
	_, err := store.Get(context.Background(), runstate.BlobRef{ID: "missing"})
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
