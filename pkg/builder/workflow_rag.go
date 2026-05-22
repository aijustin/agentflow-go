package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func ragRetrieveInput(namespace string) json.RawMessage {
	input, _ := json.Marshal(map[string]any{
		"query":     "{{prompt}}",
		"namespace": namespace,
		"limit":     RAGRetrieveLimitDefault,
	})
	return input
}

func ragRewriteInput(namespace string) json.RawMessage {
	input, _ := json.Marshal(map[string]any{
		"query_from": "steps.grade.output.rewrite_query",
		"namespace":  namespace,
		"limit":      RAGRetrieveLimitDefault,
	})
	return input
}

func ragRouteInput() json.RawMessage {
	input, _ := json.Marshal(map[string]any{"query": "{{prompt}}"})
	return input
}

func ragGradeInput(includeMinScore bool) json.RawMessage {
	spec := map[string]any{
		"query":   "{{prompt}}",
		"results": "steps.retrieve.output",
	}
	if includeMinScore {
		spec["min_score"] = RAGGradeMinScoreDefault
	}
	input, _ := json.Marshal(spec)
	return input
}

// AdaptiveRAGWorkflow builds the query_router → retrieve → answer graph from
// catalog ID adaptive-rag.
func AdaptiveRAGWorkflow(namespace, agentName string) core.Workflow {
	return NewWorkflow().
		NodeWithDepends(core.WorkflowNode{
			ID:    "route",
			Kind:  NodeQueryRouter,
			Input: ragRouteInput(),
		}).
		NodeWithDepends(core.WorkflowNode{
			ID:        "retrieve",
			Kind:      NodeTool,
			Ref:       NameKnowledgeRetrieveTool,
			Condition: ConditionRouteToRAG,
			Input:     ragRetrieveInput(namespace),
		}, "route").
		NodeWithDepends(core.WorkflowNode{
			ID:   "answer",
			Kind: NodeAgent,
			Ref:  agentName,
		}, "route", "retrieve").
		Edge("route", "retrieve").
		Edge("retrieve", "answer").
		Edge("route", "answer").
		Build()
}

// CorrectiveRAGWorkflow builds the route → retrieve → grade → rewrite → answer
// graph for catalog ID corrective-rag.
func CorrectiveRAGWorkflow(namespace, agentName string) core.Workflow {
	return NewWorkflow().
		NodeWithDepends(core.WorkflowNode{
			ID:    "route",
			Kind:  NodeQueryRouter,
			Input: ragRouteInput(),
		}).
		NodeWithDepends(core.WorkflowNode{
			ID:        "retrieve",
			Kind:      NodeTool,
			Ref:       NameKnowledgeRetrieveTool,
			Condition: ConditionRouteToRAG,
			Input:     ragRetrieveInput(namespace),
		}, "route").
		NodeWithDepends(core.WorkflowNode{
			ID:    "grade",
			Kind:  NodeRAGGrade,
			Input: ragGradeInput(true),
		}, "retrieve").
		NodeWithDepends(core.WorkflowNode{
			ID:        "rewrite",
			Kind:      NodeTool,
			Ref:       NameKnowledgeRetrieveTool,
			Condition: ConditionGradeNotRelevant,
			Input:     ragRewriteInput(namespace),
		}, "grade").
		NodeWithDepends(core.WorkflowNode{
			ID:   "answer",
			Kind: NodeAgent,
			Ref:  agentName,
		}, "grade", "rewrite").
		Edge("route", "retrieve").
		Edge("retrieve", "grade").
		Edge("grade", "rewrite").
		Edge("rewrite", "answer").
		Edge("grade", "answer").
		Build()
}

// SelfRAGWorkflow builds the retrieve → grade → answer graph for catalog ID self-rag.
func SelfRAGWorkflow(namespace, agentName string) core.Workflow {
	return NewWorkflow().
		NodeWithDepends(core.WorkflowNode{
			ID:    "retrieve",
			Kind:  NodeTool,
			Ref:   NameKnowledgeRetrieveTool,
			Input: ragRetrieveInput(namespace),
		}).
		NodeWithDepends(core.WorkflowNode{
			ID:    "grade",
			Kind:  NodeRAGGrade,
			Input: ragGradeInput(false),
		}, "retrieve").
		NodeWithDepends(core.WorkflowNode{
			ID:   "answer",
			Kind: NodeAgent,
			Ref:  agentName,
		}, "grade").
		Edge("retrieve", "grade").
		Edge("grade", "answer").
		Build()
}

// HybridResearchWorkflow builds the initial_search tool phase for catalog ID hybrid-research.
func HybridResearchWorkflow(toolRef string) core.Workflow {
	return NewWorkflow().
		NodeTool("initial_search", toolRef).
		Build()
}
