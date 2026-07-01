package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type queryRouterInput struct {
	Query string `json:"query"`
}

type queryRouterOutput struct {
	Route  string `json:"route"`
	Reason string `json:"reason,omitempty"`
}

func (r *WorkflowRunner) runQueryRouterNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	input, err := r.resolveNodeInput(ctx, runID, node.Input, scenario.Runtime.Secrets)
	if err != nil {
		return err
	}
	var spec queryRouterInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &spec)
	}
	query := strings.TrimSpace(spec.Query)
	if query == "" {
		query = strings.TrimSpace(node.Ref)
	}
	route := classifyQueryRoute(query)
	return r.saveStepOutput(ctx, scenario, runID, node.ID, queryRouterOutput{Route: route, Reason: "keyword router"})
}

func classifyQueryRoute(query string) string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "doc") || strings.Contains(q, "knowledge") || strings.Contains(q, "search") || strings.Contains(q, "retrieve"):
		return "rag"
	case strings.Contains(q, "approve") || strings.Contains(q, "review") || strings.Contains(q, "human"):
		return "hitl"
	default:
		return "direct"
	}
}

type ragGradeInput struct {
	Query    string          `json:"query"`
	Results  json.RawMessage `json:"results"`
	MinScore float64         `json:"min_score,omitempty"`
}

type ragGradeOutput struct {
	Relevant     bool    `json:"relevant"`
	Score        float64 `json:"score"`
	RewriteQuery string  `json:"rewrite_query,omitempty"`
}

func (r *WorkflowRunner) runRAGGradeNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	input, err := r.resolveNodeInput(ctx, runID, node.Input, scenario.Runtime.Secrets)
	if err != nil {
		return err
	}
	var spec ragGradeInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &spec); err != nil {
			return fmt.Errorf("orchestration: rag_grade node %q decode input: %w", node.ID, err)
		}
	}
	minScore := spec.MinScore
	if minScore <= 0 {
		minScore = 0.35
	}
	score := gradeRetrievalResults(spec.Query, spec.Results)
	out := ragGradeOutput{Relevant: score >= minScore, Score: score}
	if !out.Relevant && strings.TrimSpace(spec.Query) != "" {
		out.RewriteQuery = spec.Query + " detailed evidence"
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, out)
}

func gradeRetrievalResults(query string, results json.RawMessage) float64 {
	if len(results) == 0 {
		return 0
	}
	var payload struct {
		Results []struct {
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(results, &payload); err != nil || len(payload.Results) == 0 {
		return 0
	}
	best := 0.0
	queryTerms := strings.Fields(strings.ToLower(query))
	for _, result := range payload.Results {
		score := result.Score
		content := strings.ToLower(result.Content)
		matches := 0
		for _, term := range queryTerms {
			if strings.Contains(content, term) {
				matches++
			}
		}
		if len(queryTerms) > 0 {
			score = score*0.6 + float64(matches)/float64(len(queryTerms))*0.4
		}
		if score > best {
			best = score
		}
	}
	return best
}

type supervisorInput struct {
	Refs   []string `json:"refs"`
	Prompt string   `json:"prompt"`
}

func (r *WorkflowRunner) runSupervisorNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.agents == nil {
		return fmt.Errorf("orchestration: supervisor node %q requires agent registry", node.ID)
	}
	input, err := r.resolveNodeInput(ctx, runID, node.Input, scenario.Runtime.Secrets)
	if err != nil {
		return err
	}
	var spec supervisorInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &spec); err != nil {
			return fmt.Errorf("orchestration: supervisor node %q decode input: %w", node.ID, err)
		}
	}
	if len(spec.Refs) == 0 && node.Ref != "" {
		spec.Refs = []string{node.Ref}
	}
	agentInput := core.AgentInput{RunID: runID, Prompt: spec.Prompt}
	agentInput, amendmentApplied, err := r.resolveWorkflowAmendmentForAgent(ctx, runID, agentInput)
	if err != nil {
		return err
	}

	outputs := make(map[string]core.AgentOutput, len(spec.Refs))
	for _, ref := range spec.Refs {
		runner, ok := r.agents.Agent(ref)
		if !ok {
			return fmt.Errorf("orchestration: supervisor node %q unknown agent %q", node.ID, ref)
		}
		output, err := runner.Run(ctx, agentInput)
		if err != nil {
			return err
		}
		outputs[ref] = output
	}
	// Only clear the amendment once every delegated agent has actually
	// succeeded: clearing it beforehand would silently drop the human
	// feedback if this node is retried after a failure.
	if amendmentApplied {
		if err := r.clearWorkflowAmendment(ctx, runID); err != nil {
			return err
		}
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, outputs)
}
