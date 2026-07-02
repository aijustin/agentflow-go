package studio

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestCompareSnapshots(t *testing.T) {
	a := runstate.RunSnapshot{
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"only-a": {Inline: json.RawMessage(`{"x":1}`)},
			"shared": {Inline: json.RawMessage(`{"same":true}`)},
		},
	}
	b := runstate.RunSnapshot{
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"only-b": {Inline: json.RawMessage(`{"y":2}`)},
			"shared": {Inline: json.RawMessage(`{"same":true}`)},
			"diff":   {Inline: json.RawMessage(`{"same":false}`)},
		},
	}
	result := CompareSnapshots("run-a", "run-b", a, b)
	if len(result.StepsOnlyA) != 1 || result.StepsOnlyA[0] != "only-a" {
		t.Fatalf("unexpected only A: %+v", result.StepsOnlyA)
	}
	if len(result.StepsOnlyB) != 2 {
		t.Fatalf("unexpected only B: %+v", result.StepsOnlyB)
	}
	for _, step := range result.SharedSteps {
		if step.NodeID == "shared" && !step.Same {
			t.Fatal("expected shared step to match")
		}
		if step.NodeID == "diff" && step.Same {
			t.Fatal("expected diff step to differ")
		}
	}
}

func TestCompareSnapshotsBlobOutputs(t *testing.T) {
	a := runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{
			"blob": {Blob: &runstate.BlobRef{ID: "a", Sha256: "aaa"}},
		},
	}
	b := runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{
			"blob": {Blob: &runstate.BlobRef{ID: "b", Sha256: "bbb"}},
		},
	}
	result := CompareSnapshots("run-a", "run-b", a, b)
	if len(result.SharedSteps) != 1 || result.SharedSteps[0].Same {
		t.Fatalf("expected differing blob refs: %+v", result.SharedSteps)
	}
}

func TestCompareSnapshotsBlobMissingSide(t *testing.T) {
	a := runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{
			"blob": {Blob: &runstate.BlobRef{ID: "a"}},
		},
	}
	b := runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{
			"blob": {Inline: json.RawMessage(`{}`)},
		},
	}
	result := CompareSnapshots("run-a", "run-b", a, b)
	if len(result.SharedSteps) != 1 || result.SharedSteps[0].Same {
		t.Fatalf("expected inline vs blob mismatch: %+v", result.SharedSteps)
	}
}
