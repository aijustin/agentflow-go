package runstate

import (
	"context"
	"testing"
)

type blobAdminStub struct {
	list   []BlobRef
	deleted []string
}

func (s *blobAdminStub) Put(context.Context, []byte) (BlobRef, error) { return BlobRef{}, nil }
func (s *blobAdminStub) Get(context.Context, BlobRef) ([]byte, error) { return nil, ErrNotFound }
func (s *blobAdminStub) List(context.Context) ([]BlobRef, error)     { return s.list, nil }
func (s *blobAdminStub) Delete(_ context.Context, ref BlobRef) error {
	s.deleted = append(s.deleted, ref.ID)
	return nil
}

type repoStub struct {
	snapshots []RunSnapshot
}

func (r *repoStub) Save(context.Context, *RunSnapshot, int64) error { return nil }
func (r *repoStub) Load(context.Context, string) (RunSnapshot, error) {
	return RunSnapshot{}, ErrNotFound
}
func (r *repoStub) Delete(context.Context, string) error { return nil }
func (r *repoStub) List(context.Context, ListFilter) ([]RunSnapshot, error) {
	return r.snapshots, nil
}

func TestPurgeOrphanBlobs(t *testing.T) {
	repo := &repoStub{snapshots: []RunSnapshot{{
		RunID:  "run-1",
		Status: RunStatusCompleted,
		StepOutputs: map[string]StepOutputRef{
			"large": {Blob: &BlobRef{ID: "keep"}},
		},
	}}}
	blobs := &blobAdminStub{list: []BlobRef{{ID: "keep"}, {ID: "drop"}}}
	removed, err := PurgeOrphanBlobs(context.Background(), repo, blobs, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(blobs.deleted) != 1 || blobs.deleted[0] != "drop" {
		t.Fatalf("unexpected purge result removed=%d deleted=%v", removed, blobs.deleted)
	}
}
