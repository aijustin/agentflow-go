package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type planExecutionState struct {
	Steps []planExecutionStep `json:"steps"`
}

type planExecutionStep struct {
	Goal   string `json:"goal"`
	Tool   string `json:"tool,omitempty"`
	Status string `json:"status"`
}

func (e *Engine) persistPlan(ctx context.Context, runID string, rawPlan []byte, maxSteps int) error {
	steps := parsePlanSteps(rawPlan, maxSteps)
	if len(steps) == 0 {
		return nil
	}
	state := planExecutionState{Steps: steps}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return e.saveStepOutput(ctx, runID, "plan", json.RawMessage(raw))
}

func parsePlanSteps(rawPlan []byte, maxSteps int) []planExecutionStep {
	var plan autonomousPlan
	if err := json.Unmarshal(rawPlan, &plan); err != nil || len(plan.Steps) == 0 {
		return nil
	}
	limit := len(plan.Steps)
	if maxSteps > 0 && limit > maxSteps {
		limit = maxSteps
	}
	steps := make([]planExecutionStep, 0, limit)
	for index := 0; index < limit; index++ {
		step := plan.Steps[index]
		if step.Goal == "" {
			continue
		}
		steps = append(steps, planExecutionStep{
			Goal:   step.Goal,
			Tool:   step.Tool,
			Status: "pending",
		})
	}
	return steps
}

func (e *Engine) markPlanStepDone(ctx context.Context, runID, toolName string) error {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return nil
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		return err
	}
	updated := false
	for index := range state.Steps {
		if state.Steps[index].Status != "pending" {
			continue
		}
		if toolName != "" && state.Steps[index].Tool != "" && state.Steps[index].Tool != toolName {
			continue
		}
		state.Steps[index].Status = "done"
		updated = true
		if toolName != "" {
			break
		}
	}
	if !updated {
		return nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err = runstate.LoadAuthorized(ctx, e.runs, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		snapshot.StepOutputs["plan"] = runstate.StepOutputRef{Inline: raw}
		if err := e.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("runtime: failed to update plan execution state")
}
