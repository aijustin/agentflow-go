package graph

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

var routeIfEqPattern = regexp.MustCompile(`^eq\((.+),\s*(.+)\)$`)

type mapNodeInput struct {
	ItemsPath string          `json:"items_path"`
	Branch    mapNodeBranch   `json:"branch"`
	OnError   string          `json:"on_error,omitempty"`
	ItemField string          `json:"item_field,omitempty"`
}

type mapNodeBranch struct {
	Kind  core.WorkflowNodeKind `json:"kind"`
	Ref   string                `json:"ref,omitempty"`
	Input json.RawMessage       `json:"input,omitempty"`
}

// GenerateBuilderCode renders a builder-style Go snippet for the scenario workflow.
func GenerateBuilderCode(scenario core.Scenario) (string, error) {
	if scenario.Orchestration.Workflow == nil {
		return "", fmt.Errorf("graph: scenario workflow is required for codegen")
	}
	var b strings.Builder
	name := scenario.Name
	if name == "" {
		name = "studio-scenario"
	}
	fmt.Fprintf(&b, "package main\n\nimport (\n\t\"encoding/json\"\n\n\t\"github.com/aijustin/agentflow-go/pkg/builder\"\n\t\"github.com/aijustin/agentflow-go/pkg/core\"\n)\n\nfunc BuildScenario() core.Scenario {\n")
	if len(scenario.Orchestration.Workflows) > 0 {
		for subName, wf := range scenario.Orchestration.Workflows {
			fmt.Fprintf(&b, "\t%s := ", sanitizeIdent(subName))
			writeWorkflowBuilder(&b, wf)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\twf := ")
	writeWorkflowBuilder(&b, *scenario.Orchestration.Workflow)
	b.WriteString("\n\treturn builder.New(")
	fmt.Fprintf(&b, "%q", name)
	b.WriteString(").\n")
	switch scenario.Orchestration.Mode {
	case core.OrchestrationHybrid:
		b.WriteString("\t\tHybrid(wf).\n")
	default:
		b.WriteString("\t\tFixedWorkflow(wf).\n")
	}
	for subName := range scenario.Orchestration.Workflows {
		fmt.Fprintf(&b, "\t\tNamedWorkflow(%q, %s).\n", subName, sanitizeIdent(subName))
	}
	b.WriteString("\t\tScenario()\n}\n")
	return b.String(), nil
}

func writeWorkflowBuilder(b *strings.Builder, wf core.Workflow) {
	b.WriteString("builder.NewWorkflow()")
	for _, node := range wf.Nodes {
		switch node.Kind {
		case core.NodeAgent:
			fmt.Fprintf(b, ".\n\t\tNodeAgent(%q, %q)", node.ID, node.Ref)
		case core.NodeTool:
			fmt.Fprintf(b, ".\n\t\tNodeTool(%q, %q)", node.ID, node.Ref)
		case core.NodeSkill:
			fmt.Fprintf(b, ".\n\t\tNodeSkill(%q, %q)", node.ID, node.Ref)
		case core.NodeHumanGate:
			fmt.Fprintf(b, ".\n\t\tNodeHumanGate(%q)", node.ID)
		case core.NodeSubgraph:
			fmt.Fprintf(b, ".\n\t\tNodeSubgraph(%q, %q)", node.ID, node.Ref)
		case core.NodeMap:
			writeMapNode(b, node)
		case core.NodeLoop:
			fmt.Fprintf(b, ".\n\t\tNodeLoop(%q, %s)", node.ID, rawJSONLiteral(node.Input))
		case core.NodeParallelGroup:
			fmt.Fprintf(b, ".\n\t\tNodeParallelGroup(%q, %s)", node.ID, rawJSONLiteral(node.Input))
		case core.NodeTransform:
			fmt.Fprintf(b, ".\n\t\tNodeTransform(%q, %s)", node.ID, rawJSONLiteral(node.Input))
		default:
			fmt.Fprintf(b, ".\n\t\tNodeTransform(%q, %s) // kind %q", node.ID, rawJSONLiteral(node.Input), node.Kind)
		}
	}
	for _, edge := range wf.Edges {
		writeEdge(b, edge)
	}
	b.WriteString(".\n\t\tBuild()")
}

func writeMapNode(b *strings.Builder, node core.WorkflowNode) {
	var spec mapNodeInput
	if err := json.Unmarshal(node.Input, &spec); err != nil || spec.ItemsPath == "" {
		fmt.Fprintf(b, ".\n\t\tNodeMap(%q, %s)", node.ID, rawJSONLiteral(node.Input))
		return
	}
	itemsPath, ok := formatStepPathExpr(spec.ItemsPath)
	if !ok {
		fmt.Fprintf(b, ".\n\t\tNodeMap(%q, %s)", node.ID, rawJSONLiteral(node.Input))
		return
	}
	branch := formatMapBranch(spec.Branch)
	var opts []string
	if spec.OnError != "" {
		opts = append(opts, fmt.Sprintf("builder.MapOnError(%q)", spec.OnError))
	}
	if spec.ItemField != "" {
		opts = append(opts, fmt.Sprintf("builder.MapItemField(%q)", spec.ItemField))
	}
	if len(opts) == 0 {
		fmt.Fprintf(b, ".\n\t\tMapOver(%q, %s, %s)", node.ID, itemsPath, branch)
		return
	}
	fmt.Fprintf(b, ".\n\t\tMapOver(%q, %s, %s, %s)", node.ID, itemsPath, branch, strings.Join(opts, ", "))
}

func formatMapBranch(branch mapNodeBranch) string {
	switch branch.Kind {
	case core.NodeAgent:
		return fmt.Sprintf("builder.MapAgentBranch(%q)", branch.Ref)
	case core.NodeTool:
		return fmt.Sprintf("builder.MapToolBranch(%q)", branch.Ref)
	case core.NodeSubgraph:
		return fmt.Sprintf("builder.MapSubgraphBranch(%q)", branch.Ref)
	case core.NodeTransform:
		return fmt.Sprintf("builder.MapTransformBranch(%s)", rawJSONLiteral(branch.Input))
	default:
		return fmt.Sprintf("builder.MapBranch{Kind: builder.NodeTransform, Input: %s}", rawJSONLiteral(branch.Input))
	}
}

func writeEdge(b *strings.Builder, edge core.WorkflowEdge) {
	condition := strings.TrimSpace(edge.Condition)
	if condition == "" {
		fmt.Fprintf(b, ".\n\t\tEdge(%q, %q)", edge.From, edge.To)
		return
	}
	if path, value, ok := parseRouteIfEq(condition); ok {
		fmt.Fprintf(b, ".\n\t\tRouteIf(%q, %q, %s, %s)", edge.From, edge.To, path, value)
		return
	}
	fmt.Fprintf(b, ".\n\t\tEdgeIf(%q, %q, %q)", edge.From, edge.To, condition)
}

func parseRouteIfEq(condition string) (pathExpr, valueExpr string, ok bool) {
	matches := routeIfEqPattern.FindStringSubmatch(condition)
	if len(matches) != 3 {
		return "", "", false
	}
	path := strings.TrimSpace(matches[1])
	value := strings.TrimSpace(matches[2])
	stepPath, ok := formatStepPathExpr(path)
	if !ok {
		return "", "", false
	}
	return stepPath, value, true
}

func formatStepPathExpr(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "steps.") {
		return strconv.Quote(path), false
	}
	rest := strings.TrimPrefix(path, "steps.")
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || parts[0] == "" {
		return strconv.Quote(path), false
	}
	args := make([]string, len(parts))
	for i, part := range parts {
		args[i] = strconv.Quote(part)
	}
	return "builder.StepPath(" + strings.Join(args, ", ") + ")", true
}

func sanitizeIdent(name string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, name)
	if out == "" {
		return "subgraph"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "wf_" + out
	}
	return out
}
