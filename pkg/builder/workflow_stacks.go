package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func parallelGroupRefsInput(refs ...string) json.RawMessage {
	input, _ := json.Marshal(map[string]any{"refs": refs})
	return input
}

func parallelGroupToolsInput(onError string, tools ...string) json.RawMessage {
	spec := map[string]any{"tools": tools}
	if onError != "" {
		spec["on_error"] = onError
	}
	input, _ := json.Marshal(spec)
	return input
}

func statusReadyInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{"message": "ready"})
	return input
}

func echoBranchInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{
		"copy_from":   map[string]string{"message": "steps.status.output.message"},
		"prompt_from": "steps.status.output.message",
	})
	return input
}

func gitDiffInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{
		"action": "diff",
		"repo":   ".",
	})
	return input
}

func mergeReviewFindingsInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{
		"copy": map[string]string{
			"findings": "steps.reviews.members",
		},
	})
	return input
}

// MultiExpertResearchWorkflow builds the parallel expert phase from
// catalog ID multi-expert-research.
func MultiExpertResearchWorkflow(expertAgents ...string) core.Workflow {
	return NewWorkflow().
		NodeParallelGroup("experts", parallelGroupRefsInput(expertAgents...)).
		Build()
}

// CodeReviewPipelineWorkflow builds the diff → reviews → merge → approve graph
// for catalog ID code-review-pipeline.
func CodeReviewPipelineWorkflow(gitTool string, reviewerAgents ...string) core.Workflow {
	return NewWorkflow().
		NodeWithDepends(core.WorkflowNode{
			ID:    "diff",
			Kind:  NodeTool,
			Ref:   gitTool,
			Input: gitDiffInput(),
		}).
		NodeWithDepends(core.WorkflowNode{
			ID:    "reviews",
			Kind:  NodeParallelGroup,
			Input: parallelGroupRefsInput(reviewerAgents...),
		}, "diff").
		NodeWithDepends(core.WorkflowNode{
			ID:    "merge",
			Kind:  NodeTransform,
			Input: mergeReviewFindingsInput(),
		}, "reviews").
		NodeWithDepends(core.WorkflowNode{
			ID:   "approve",
			Kind: NodeHumanGate,
		}, "merge").
		Build()
}

// WorkflowEnhancementsWorkflow builds the status → ready_branch → experts graph
// for catalog ID workflow-enhancements.
func WorkflowEnhancementsWorkflow() core.Workflow {
	return NewWorkflow().
		NodeWithDepends(core.WorkflowNode{
			ID:    "status",
			Kind:  NodeTool,
			Ref:   NameStatusTool,
			Input: statusReadyInput(),
		}).
		NodeWithDepends(core.WorkflowNode{
			ID:    "ready_branch",
			Kind:  NodeTool,
			Ref:   NameEchoTool,
			Input: echoBranchInput(),
		}).
		NodeParallelGroup("experts", parallelGroupToolsInput(ParallelOnErrorCollectErrors, NameStatusTool, NameEchoTool)).
		EdgeIf("status", "ready_branch", ConditionStatusReady).
		Build()
}

// FixedWorkflowReviewWorkflow builds the inspect → review graph from
// catalog ID fixed-workflow-review.
func FixedWorkflowReviewWorkflow(toolRef, agentRef string) core.Workflow {
	return NewWorkflow().
		NodeTool("inspect", toolRef).
		NodeAgent("review", agentRef).
		Edge("inspect", "review").
		Build()
}

func prepareReadyInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{"set": map[string]any{"ready": true}})
	return input
}

func continueAfterInterruptInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{"set": map[string]any{"continued": true}})
	return input
}

// DeclarativeInterruptWorkflow builds prepare → interrupt → continue for catalog ID declarative-interrupt.
func DeclarativeInterruptWorkflow() core.Workflow {
	return NewWorkflow().
		NodeTransform("prepare", prepareReadyInput()).
		WithInterrupt().
		NodeTransform("continue", continueAfterInterruptInput()).
		Edge("prepare", "continue").
		Build()
}
