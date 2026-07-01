package orchestration

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	workflowAmendmentVar = "workflow_amendment"
	resumePromptVar      = "resume_prompt"
)

func snapshotVariableString(vars map[string]json.RawMessage, key string) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func applyWorkflowAmendmentToAgentInput(snapshot runstate.RunSnapshot, runID string, resolvedInput json.RawMessage) (core.AgentInput, bool) {
	input := coreAgentInputFromResolved(resolvedInput)
	input.RunID = runID
	return applyWorkflowAmendment(snapshot, input)
}

// applyWorkflowAmendment appends any pending human amendment (left by a
// gate resume) to input.Prompt and reports whether one was applied. The
// caller is responsible for clearing workflowAmendmentVar once applied, so
// the amendment reaches only the node(s) that resume directly into and is
// not silently re-applied to every later node for the rest of the run.
func applyWorkflowAmendment(snapshot runstate.RunSnapshot, input core.AgentInput) (core.AgentInput, bool) {
	amendment := snapshotVariableString(snapshot.Variables, workflowAmendmentVar)
	if amendment == "" {
		return input, false
	}
	if input.Prompt != "" {
		input.Prompt = input.Prompt + "\n\nHuman feedback: " + amendment
	} else {
		input.Prompt = amendment
	}
	return input, true
}

// clearWorkflowAmendment removes the pending amendment after it has been
// applied to a node's input, so a human's feedback on one gate reaches only
// the node(s) that resume directly into rather than being appended to every
// agent/supervisor node for the remainder of the run.
func (r *WorkflowRunner) clearWorkflowAmendment(ctx context.Context, runID string) error {
	if r.runs == nil {
		return nil
	}
	return r.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			return nil
		}
		delete(snapshot.Variables, workflowAmendmentVar)
		return nil
	})
}

func coreAgentInputFromResolved(resolvedInput json.RawMessage) core.AgentInput {
	input := core.AgentInput{Context: resolvedInput}
	if len(resolvedInput) == 0 {
		return input
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(resolvedInput, &fields); err != nil {
		return input
	}
	if raw, ok := fields["prompt"]; ok {
		_ = json.Unmarshal(raw, &input.Prompt)
	}
	return input
}
