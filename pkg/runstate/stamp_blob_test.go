package runstate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStampSnapshotPreservesCreatedAt(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	snapshot := &RunSnapshot{RunID: "run-1"}
	previous := &RunSnapshot{RunID: "run-1", CreatedAt: created, UpdatedAt: created}
	StampSnapshot(snapshot, previous, updated)
	if !snapshot.CreatedAt.Equal(created) || !snapshot.UpdatedAt.Equal(updated) {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestCollectBlobRefsIncludesVariableBlobRef(t *testing.T) {
	ref := StepOutputRef{Blob: &BlobRef{ID: "blob-var", Sha256: "abc"}}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	refs := CollectBlobRefs(RunSnapshot{
		Variables: map[string]json.RawMessage{"checkpoint_state": raw},
	})
	if len(refs) != 1 || refs["blob-var"].ID != "blob-var" {
		t.Fatalf("refs=%+v", refs)
	}
}
