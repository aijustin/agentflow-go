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
	// A replan overwrites the "plan" step output with a brand new plan.
	// Carry forward already-completed progress (keyed by tool name) from
	// any prior plan so replanning does not make finished work look
	// pending again, which would otherwise risk re-executing tools that
	// already ran and could keep the loop replanning indefinitely.
	doneToolCounts := e.donePlanStepToolCounts(ctx, runID)
	for index := range steps {
		tool := steps[index].Tool
		if tool == "" || doneToolCounts[tool] <= 0 {
			continue
		}
		steps[index].Status = "done"
		doneToolCounts[tool]--
	}
	state := planExecutionState{Steps: steps}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return e.saveStepOutput(ctx, runID, "plan", json.RawMessage(raw))
}

func (e *Engine) donePlanStepToolCounts(ctx context.Context, runID string) map[string]int {
	counts := make(map[string]int)
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return counts
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return counts
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		return counts
	}
	for _, step := range state.Steps {
		if step.Status == "done" && step.Tool != "" {
			counts[step.Tool]++
		}
	}
	return counts
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

// markPlanStepDoneInState marks exactly one pending step in state done for a
// completed tool call and reports whether it changed anything. It prefers an
// exact tool-name match among pending steps - so calling tools out of plan
// order still credits the right step, regardless of position - and only
// falls back to the first pending step that names no specific tool. Without
// this ordering, a "wildcard" (tool-less) step earlier in the list would
// silently absorb completion credit for any tool call, even when a later,
// still-pending step explicitly names that exact tool.
func markPlanStepDoneInState(state *planExecutionState, toolName string) bool {
	if toolName != "" {
		for index := range state.Steps {
			if state.Steps[index].Status == "pending" && state.Steps[index].Tool == toolName {
				state.Steps[index].Status = "done"
				return true
			}
		}
	}
	for index := range state.Steps {
		if state.Steps[index].Status == "pending" && state.Steps[index].Tool == "" {
			state.Steps[index].Status = "done"
			return true
		}
	}
	return false
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
	if !markPlanStepDoneInState(&state, toolName) {
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
