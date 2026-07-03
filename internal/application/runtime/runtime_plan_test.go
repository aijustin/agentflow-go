package runtime

import (
	"context"
	"encoding/json"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestMarkPlanStepDoneInStatePrefersNamedTool(t *testing.T) {
	state := planExecutionState{
		Steps: []planExecutionStep{
			{Tool: "", Status: "pending"},
			{Tool: "echo", Status: "pending"},
		},
	}
	if !markPlanStepDoneInState(&state, "echo") {
		t.Fatal("expected named tool step to be marked done")
	}
	if state.Steps[0].Status != "pending" || state.Steps[1].Status != "done" {
		t.Fatalf("unexpected steps: %+v", state.Steps)
	}
}

func TestEngineMarkPlanStepDonePersistsUpdatedPlan(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	planRaw, err := json.Marshal(planExecutionState{
		Steps: []planExecutionStep{{Tool: "echo", Status: "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-plan-done", Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"plan": {Inline: planRaw},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.markPlanStepDone(ctx, "run-plan-done", "echo"); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(ctx, "run-plan-done")
	if err != nil {
		t.Fatal(err)
	}
	var state planExecutionState
	if err := json.Unmarshal(loaded.StepOutputs["plan"].Inline, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Steps) != 1 || state.Steps[0].Status != "done" {
		t.Fatalf("expected done plan step, got %+v", state)
	}
}
