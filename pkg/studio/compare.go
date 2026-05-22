package studio

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// StepDiff describes one workflow step output comparison.
type StepDiff struct {
	NodeID  string          `json:"node_id"`
	Same    bool            `json:"same"`
	OutputA json.RawMessage `json:"output_a,omitempty"`
	OutputB json.RawMessage `json:"output_b,omitempty"`
}

// RunCompareResult summarizes differences between two run snapshots.
type RunCompareResult struct {
	RunA        string             `json:"run_a"`
	RunB        string             `json:"run_b"`
	StatusA     runstate.RunStatus `json:"status_a"`
	StatusB     runstate.RunStatus `json:"status_b"`
	StepsOnlyA  []string           `json:"steps_only_a"`
	StepsOnlyB  []string           `json:"steps_only_b"`
	SharedSteps []StepDiff         `json:"shared_steps"`
}

// CompareSnapshots diffs step outputs between two run snapshots.
func CompareSnapshots(runA, runB string, a, b runstate.RunSnapshot) RunCompareResult {
	onlyA, onlyB, shared := diffStepIDs(a.StepOutputs, b.StepOutputs)
	out := RunCompareResult{
		RunA:       runA,
		RunB:       runB,
		StatusA:    a.Status,
		StatusB:    b.Status,
		StepsOnlyA: onlyA,
		StepsOnlyB: onlyB,
	}
	for _, nodeID := range shared {
		refA := a.StepOutputs[nodeID]
		refB := b.StepOutputs[nodeID]
		out.SharedSteps = append(out.SharedSteps, StepDiff{
			NodeID:  nodeID,
			Same:    sameOutput(refA, refB),
			OutputA: refA.Inline,
			OutputB: refB.Inline,
		})
	}
	return out
}

func diffStepIDs(a, b map[string]runstate.StepOutputRef) (onlyA, onlyB, shared []string) {
	seen := make(map[string]struct{})
	for id := range a {
		seen[id] = struct{}{}
	}
	for id := range b {
		seen[id] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		_, inA := a[id]
		_, inB := b[id]
		switch {
		case inA && inB:
			shared = append(shared, id)
		case inA:
			onlyA = append(onlyA, id)
		case inB:
			onlyB = append(onlyB, id)
		}
	}
	return onlyA, onlyB, shared
}

func sameOutput(a, b runstate.StepOutputRef) bool {
	if a.Blob != nil || b.Blob != nil {
		if a.Blob == nil || b.Blob == nil {
			return false
		}
		return a.Blob.ID == b.Blob.ID && a.Blob.Sha256 == b.Blob.Sha256
	}
	return bytes.Equal(a.Inline, b.Inline)
}
