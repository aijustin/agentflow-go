package file

import (
	"bytes"
	"context"
	"testing"
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
