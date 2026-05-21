package runstate

import "testing"

func TestCollectReferencedBlobIDs(t *testing.T) {
	snapshots := []RunSnapshot{{
		RunID:  "run-1",
		Status: RunStatusCompleted,
		StepOutputs: map[string]StepOutputRef{
			"large": {Blob: &BlobRef{ID: "blob-a", Sha256: "blob-a"}},
		},
	}, {
		RunID:  "run-2",
		Status: RunStatusCompleted,
		StepOutputs: map[string]StepOutputRef{
			"other": {Blob: &BlobRef{ID: "blob-b", Sha256: "blob-b"}},
		},
	}}
	referenced := CollectReferencedBlobIDs(snapshots)
	if len(referenced) != 2 {
		t.Fatalf("expected two referenced blobs, got %d", len(referenced))
	}
	if _, ok := referenced["blob-a"]; !ok {
		t.Fatal("missing blob-a")
	}
}
