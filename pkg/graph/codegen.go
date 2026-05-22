package graph

import (
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

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
			fmt.Fprintf(b, ".\n\t\tNodeMap(%q, %s)", node.ID, rawJSONLiteral(node.Input))
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
		if strings.TrimSpace(edge.Condition) != "" {
			fmt.Fprintf(b, ".\n\t\tEdgeIf(%q, %q, %q)", edge.From, edge.To, edge.Condition)
		} else {
			fmt.Fprintf(b, ".\n\t\tEdge(%q, %q)", edge.From, edge.To)
		}
	}
	b.WriteString(".\n\t\tBuild()")
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
