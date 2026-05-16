package yaml

import (
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

func Validate(s core.Scenario) error {
	if s.Name == "" {
		return fmt.Errorf("config: scenario.name is required")
	}
	if len(s.Agents) == 0 {
		return fmt.Errorf("config: at least one agent is required")
	}
	for name, profile := range s.LLMs {
		if profile.Provider == "" {
			return fmt.Errorf("config: llms.%s.provider is required", name)
		}
		if profile.Model == "" {
			return fmt.Errorf("config: llms.%s.model is required", name)
		}
	}
	for name, mem := range s.Memories {
		if mem.Type == "" {
			return fmt.Errorf("config: memories.%s.type is required", name)
		}
		switch mem.Type {
		case "in_memory", "file", "custom":
		default:
			return fmt.Errorf("config: memories.%s.type %q is unsupported", name, mem.Type)
		}
		if mem.Scope == "" {
			return fmt.Errorf("config: memories.%s.scope is required", name)
		}
		switch memory.Scope(mem.Scope) {
		case memory.ScopeConversation, memory.ScopeSession, memory.ScopeLongTerm, memory.ScopeAudit:
		default:
			return fmt.Errorf("config: memories.%s.scope %q is unsupported", name, mem.Scope)
		}
	}
	for name, tool := range s.Tools {
		if tool.Type == "" {
			return fmt.Errorf("config: tools.%s.type is required", name)
		}
		switch tool.Approval {
		case "", core.ApprovalNever, core.ApprovalRisky, core.ApprovalAlways:
		default:
			return fmt.Errorf("config: tools.%s.approval %q is unsupported", name, tool.Approval)
		}
		switch tool.SideEffect {
		case "", core.SideEffectNone, core.SideEffectRead, core.SideEffectWrite, core.SideEffectExternal, core.SideEffectDangerous:
		default:
			return fmt.Errorf("config: tools.%s.side_effect %q is unsupported", name, tool.SideEffect)
		}
		if tool.RateCap < 0 {
			return fmt.Errorf("config: tools.%s.rate_cap must be >= 0", name)
		}
		if tool.LLM != "" {
			if _, ok := s.LLMs[tool.LLM]; !ok {
				return fmt.Errorf("config: tools.%s.llm references unknown llm %q", name, tool.LLM)
			}
		}
	}
	for name, agent := range s.Agents {
		if agent.LLM != "" {
			if _, ok := s.LLMs[agent.LLM]; !ok {
				return fmt.Errorf("config: agents.%s.llm references unknown llm %q", name, agent.LLM)
			}
		}
		if agent.Memory != "" {
			if _, ok := s.Memories[agent.Memory]; !ok {
				return fmt.Errorf("config: agents.%s.memory references unknown memory %q", name, agent.Memory)
			}
		}
		for _, tool := range agent.Tools {
			if _, ok := s.Tools[tool]; !ok {
				return fmt.Errorf("config: agents.%s.tools references unknown tool %q", name, tool)
			}
		}
		for _, skill := range agent.Skills {
			if _, ok := s.Skills[skill]; !ok {
				return fmt.Errorf("config: agents.%s.skills references unknown skill %q", name, skill)
			}
		}
	}
	switch s.Orchestration.Mode {
	case "", core.OrchestrationAutonomous, core.OrchestrationHybrid:
		return nil
	case core.OrchestrationFixedWorkflow:
		if s.Orchestration.Workflow == nil {
			return fmt.Errorf("config: fixed_workflow requires orchestration.workflow")
		}
		return validateWorkflow(*s.Orchestration.Workflow, s)
	default:
		return fmt.Errorf("config: unsupported orchestration.mode %q", s.Orchestration.Mode)
	}
}

func validateWorkflow(w core.Workflow, s core.Scenario) error {
	nodes := make(map[string]core.WorkflowNode, len(w.Nodes))
	for _, node := range w.Nodes {
		if node.ID == "" {
			return fmt.Errorf("config: workflow node id is required")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("config: duplicate workflow node %q", node.ID)
		}
		if err := validateWorkflowNode(node, s); err != nil {
			return err
		}
		nodes[node.ID] = node
	}
	for _, node := range w.Nodes {
		for _, dep := range node.DependsOn {
			if _, ok := nodes[dep]; !ok {
				return fmt.Errorf("config: workflow node %q depends_on unknown node %q", node.ID, dep)
			}
		}
	}
	for _, edge := range w.Edges {
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("config: workflow edge references unknown from node %q", edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("config: workflow edge references unknown to node %q", edge.To)
		}
	}
	adj := make(map[string][]string, len(nodes))
	for _, edge := range w.Edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}
	for _, node := range w.Nodes {
		for _, dep := range node.DependsOn {
			adj[dep] = append(adj[dep], node.ID)
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("config: workflow contains cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, next := range adj[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowNode(node core.WorkflowNode, s core.Scenario) error {
	switch node.Kind {
	case core.NodeTool:
		if node.Ref == "" {
			return fmt.Errorf("config: workflow node %q tool ref is required", node.ID)
		}
		if _, ok := s.Tools[node.Ref]; !ok {
			return fmt.Errorf("config: workflow node %q references unknown tool %q", node.ID, node.Ref)
		}
	case core.NodeAgent:
		if node.Ref == "" {
			return fmt.Errorf("config: workflow node %q agent ref is required", node.ID)
		}
		if _, ok := s.Agents[node.Ref]; !ok {
			return fmt.Errorf("config: workflow node %q references unknown agent %q", node.ID, node.Ref)
		}
	case core.NodeSkill:
		if node.Ref == "" {
			return fmt.Errorf("config: workflow node %q skill ref is required", node.ID)
		}
		if _, ok := s.Skills[node.Ref]; !ok {
			return fmt.Errorf("config: workflow node %q references unknown skill %q", node.ID, node.Ref)
		}
	case core.NodeHumanGate, core.NodeTransform:
		return nil
	default:
		return fmt.Errorf("config: workflow node %q kind %q is unsupported", node.ID, node.Kind)
	}
	return nil
}
