package runstate

// CollectBlobRefs returns blob references referenced by a snapshot's step outputs.
func CollectBlobRefs(snapshot RunSnapshot) map[string]BlobRef {
	refs := make(map[string]BlobRef)
	for _, output := range snapshot.StepOutputs {
		if output.Blob != nil && output.Blob.ID != "" {
			refs[output.Blob.ID] = *output.Blob
		}
	}
	return refs
}

// CollectReferencedBlobIDs returns blob IDs still referenced by the given snapshots.
func CollectReferencedBlobIDs(snapshots []RunSnapshot) map[string]struct{} {
	referenced := make(map[string]struct{})
	for _, snapshot := range snapshots {
		for id := range CollectBlobRefs(snapshot) {
			referenced[id] = struct{}{}
		}
	}
	return referenced
}
