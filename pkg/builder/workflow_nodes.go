package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// MapBranch describes the per-item branch executed by a map node.
type MapBranch struct {
	Kind  core.WorkflowNodeKind `json:"kind"`
	Ref   string                `json:"ref,omitempty"`
	Input json.RawMessage       `json:"input,omitempty"`
}

type mapNodeSpec struct {
	ItemsPath string    `json:"items_path"`
	Branch    MapBranch `json:"branch"`
	OnError   string    `json:"on_error,omitempty"`
	ItemField string    `json:"item_field,omitempty"`
}

// MapNodeInput builds JSON input for a map workflow node.
func MapNodeInput(itemsPath string, branch MapBranch, opts ...MapNodeOption) json.RawMessage {
	spec := mapNodeSpec{
		ItemsPath: itemsPath,
		Branch:    branch,
	}
	for _, opt := range opts {
		opt(&spec)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// MapNodeOption configures optional map node input fields.
type MapNodeOption func(*mapNodeSpec)

// MapOnError sets map node on_error (for example collect_errors).
func MapOnError(mode string) MapNodeOption {
	return func(spec *mapNodeSpec) {
		spec.OnError = mode
	}
}

// MapItemField overrides the injected item field name (default item).
func MapItemField(field string) MapNodeOption {
	return func(spec *mapNodeSpec) {
		spec.ItemField = field
	}
}

// NodeSubgraph adds a runtime subgraph node referencing orchestration.workflows[ref].
func (w *WorkflowBuilder) NodeSubgraph(id, ref string) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:   id,
		Kind: core.NodeSubgraph,
		Ref:  ref,
	})
	return w
}

// NodeMap adds a dynamic fan-out map node.
func (w *WorkflowBuilder) NodeMap(id string, input json.RawMessage) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:    id,
		Kind:  core.NodeMap,
		Input: input,
	})
	return w
}

// NodeLoop adds a loop node with JSON input payload.
func (w *WorkflowBuilder) NodeLoop(id string, input json.RawMessage) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:    id,
		Kind:  core.NodeLoop,
		Input: input,
	})
	return w
}
