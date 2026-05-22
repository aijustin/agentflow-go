package async

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

const MemoryReconcileJobType = "memory.reconcile"

type MemoryReconcilePayload struct {
	MemoryName string             `json:"memory_name"`
	Agent      string             `json:"agent"`
	RunID      string             `json:"run_id,omitempty"`
	Principal  identity.Principal `json:"principal,omitempty"`
}

func (payload MemoryReconcilePayload) MarshalJSONBytes() (json.RawMessage, error) {
	return json.Marshal(payload)
}
