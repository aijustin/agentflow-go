package orchestration

import (
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

func applyWorkflowAmendmentToAgentInput(snapshot runstate.RunSnapshot, runID string, resolvedInput json.RawMessage) core.AgentInput {
	input := coreAgentInputFromResolved(resolvedInput)
	input.RunID = runID
	amendment := snapshotVariableString(snapshot.Variables, workflowAmendmentVar)
	if amendment == "" {
		return input
	}
	if input.Prompt != "" {
		input.Prompt = input.Prompt + "\n\nHuman feedback: " + amendment
	} else {
		input.Prompt = amendment
	}
	return input
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
