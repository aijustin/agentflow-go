package runstate

import (
	"bytes"
	"encoding/json"
)

// CollectBlobRefs returns blob references referenced by a snapshot's step
// outputs and variables. Variables can carry externalized payloads (e.g.
// checkpoint state persisted by the runtime) serialized as StepOutputRef, so
// they must count as live references or orphan purges would delete blobs a
// paused run still needs to resume.
func CollectBlobRefs(snapshot RunSnapshot) map[string]BlobRef {
	refs := make(map[string]BlobRef)
	for _, output := range snapshot.StepOutputs {
		if output.Blob != nil && output.Blob.ID != "" {
			refs[output.Blob.ID] = *output.Blob
		}
	}
	for _, raw := range snapshot.Variables {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}
		var ref StepOutputRef
		if err := json.Unmarshal(trimmed, &ref); err != nil {
			continue
		}
		if ref.Blob != nil && ref.Blob.ID != "" {
			refs[ref.Blob.ID] = *ref.Blob
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
