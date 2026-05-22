package studio

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestCompareSnapshots(t *testing.T) {
	a := runstate.RunSnapshot{
		RunID:  "run-a",
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"seed": {Inline: json.RawMessage(`{"ok":true}`)},
			"only_a": {Inline: json.RawMessage(`{"a":1}`)},
		},
	}
	b := runstate.RunSnapshot{
		RunID:  "run-b",
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"seed": {Inline: json.RawMessage(`{"ok":true}`)},
			"only_b": {Inline: json.RawMessage(`{"b":2}`)},
		},
	}
	result := CompareSnapshots("run-a", "run-b", a, b)
	if len(result.StepsOnlyA) != 1 || result.StepsOnlyA[0] != "only_a" {
		t.Fatalf("only_a=%+v", result.StepsOnlyA)
	}
	if len(result.StepsOnlyB) != 1 || result.StepsOnlyB[0] != "only_b" {
		t.Fatalf("only_b=%+v", result.StepsOnlyB)
	}
	if len(result.SharedSteps) != 1 || !result.SharedSteps[0].Same {
		t.Fatalf("shared=%+v", result.SharedSteps)
	}
}
