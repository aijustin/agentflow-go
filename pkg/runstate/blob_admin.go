package runstate

import (
	"context"
	"fmt"
)

// BlobAdmin extends BlobStore with listing and deletion for retention workflows.
type BlobAdmin interface {
	BlobStore
	List(ctx context.Context) ([]BlobRef, error)
	Delete(ctx context.Context, ref BlobRef) error
}

// PurgeOrphanBlobs deletes blobs that are not referenced by any listed run snapshot.
func PurgeOrphanBlobs(ctx context.Context, runs Repository, blobs BlobStore, filter ListFilter) (int, error) {
	if runs == nil || blobs == nil {
		return 0, nil
	}
	admin, ok := blobs.(BlobAdmin)
	if !ok {
		return 0, fmt.Errorf("runstate: blob store does not support orphan purge")
	}
	snapshots, err := runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	referenced := CollectReferencedBlobIDs(snapshots)
	stored, err := admin.List(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, ref := range stored {
		if ref.ID == "" {
			continue
		}
		if _, ok := referenced[ref.ID]; ok {
			continue
		}
		if err := admin.Delete(ctx, ref); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
