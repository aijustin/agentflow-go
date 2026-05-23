package builder

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// StepPath builds a workflow expression path: steps.<nodeID>.<fields...>.
func StepPath(nodeID string, fields ...string) string {
	path := "steps." + strings.TrimSpace(nodeID)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		path += "." + field
	}
	return path
}

// ConditionEq returns eq(path, value) for workflow edge/node conditions.
func ConditionEq(path string, value any) string {
	return fmt.Sprintf("eq(%s, %s)", strings.TrimSpace(path), formatConditionValue(value))
}

// ConditionNe returns ne(path, value) for workflow edge/node conditions.
func ConditionNe(path string, value any) string {
	return fmt.Sprintf("ne(%s, %s)", strings.TrimSpace(path), formatConditionValue(value))
}

// ConditionExists returns exists(path) when a step field must be present.
func ConditionExists(path string) string {
	return fmt.Sprintf("exists(%s)", strings.TrimSpace(path))
}

// ConditionMissing returns missing(path) when a step field must be absent.
func ConditionMissing(path string) string {
	return fmt.Sprintf("missing(%s)", strings.TrimSpace(path))
}

func formatConditionValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strconv.Quote(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// MapAgentBranch configures a map fan-out branch that runs an agent node ref.
func MapAgentBranch(agentRef string) MapBranch {
	return MapBranch{Kind: core.NodeAgent, Ref: agentRef}
}

// MapToolBranch configures a map fan-out branch that runs a tool node ref.
func MapToolBranch(toolRef string) MapBranch {
	return MapBranch{Kind: core.NodeTool, Ref: toolRef}
}

// MapSubgraphBranch configures a map fan-out branch that runs a named subgraph.
func MapSubgraphBranch(subgraphRef string) MapBranch {
	return MapBranch{Kind: core.NodeSubgraph, Ref: subgraphRef}
}

// MapTransformBranch configures a map fan-out branch with a transform input payload.
func MapTransformBranch(input []byte) MapBranch {
	return MapBranch{Kind: core.NodeTransform, Input: input}
}

// MapOver adds a dynamic fan-out map node (LangGraph Send-style) over itemsPath.
func (w *WorkflowBuilder) MapOver(id, itemsPath string, branch MapBranch, opts ...MapNodeOption) *WorkflowBuilder {
	return w.NodeMap(id, MapNodeInput(itemsPath, branch, opts...))
}

// RouteIf adds a conditional edge when eq(path, value).
func (w *WorkflowBuilder) RouteIf(from, to, path string, value any) *WorkflowBuilder {
	return w.EdgeIf(from, to, ConditionEq(path, value))
}

// RouteIfNe adds a conditional edge when ne(path, value).
func (w *WorkflowBuilder) RouteIfNe(from, to, path string, value any) *WorkflowBuilder {
	return w.EdgeIf(from, to, ConditionNe(path, value))
}

// RouteIfExists adds a conditional edge when exists(path).
func (w *WorkflowBuilder) RouteIfExists(from, to, path string) *WorkflowBuilder {
	return w.EdgeIf(from, to, ConditionExists(path))
}

// RouteIfMissing adds a conditional edge when missing(path).
func (w *WorkflowBuilder) RouteIfMissing(from, to, path string) *WorkflowBuilder {
	return w.EdgeIf(from, to, ConditionMissing(path))
}

// ParallelGroup adds a parallel_group node over agent refs.
func (w *WorkflowBuilder) ParallelGroup(id string, refs ...string) *WorkflowBuilder {
	return w.NodeParallelGroup(id, parallelGroupRefsInput(refs...))
}

// ParallelTools adds a parallel_group node over tool refs.
func (w *WorkflowBuilder) ParallelTools(id, onError string, tools ...string) *WorkflowBuilder {
	return w.NodeParallelGroup(id, parallelGroupToolsInput(onError, tools...))
}
