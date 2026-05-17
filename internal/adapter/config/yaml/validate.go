package yaml

import (
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
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
		if !containsString(supportedMemoryTypes, mem.Type) {
			return fmt.Errorf("config: memories.%s.type %q is unsupported", name, mem.Type)
		}
		if mem.Scope == "" {
			return fmt.Errorf("config: memories.%s.scope is required", name)
		}
		if !containsString(supportedMemoryScopes, mem.Scope) {
			return fmt.Errorf("config: memories.%s.scope %q is unsupported", name, mem.Scope)
		}
	}
	for name, tool := range s.Tools {
		if tool.Type == "" {
			return fmt.Errorf("config: tools.%s.type is required", name)
		}
		if tool.Approval != "" && !containsString(supportedApprovalPolicies, string(tool.Approval)) {
			return fmt.Errorf("config: tools.%s.approval %q is unsupported", name, tool.Approval)
		}
		if tool.SideEffect != "" && !containsString(supportedSideEffects, string(tool.SideEffect)) {
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
	for name, skill := range s.Skills {
		for _, agent := range skill.CompatibleAgents {
			if _, ok := s.Agents[agent]; !ok {
				return fmt.Errorf("config: skills.%s.compatible_agents references unknown agent %q", name, agent)
			}
		}
		for _, policy := range skill.ToolPolicies {
			if policy.Tool == "" {
				return fmt.Errorf("config: skills.%s.tool_policies tool is required", name)
			}
			if _, ok := s.Tools[policy.Tool]; !ok {
				return fmt.Errorf("config: skills.%s.tool_policies references unknown tool %q", name, policy.Tool)
			}
			if policy.Approval != "" && !containsString(supportedApprovalPolicies, string(policy.Approval)) {
				return fmt.Errorf("config: skills.%s.tool_policies approval %q is unsupported", name, policy.Approval)
			}
			if policy.SideEffect != "" && !containsString(supportedSideEffects, string(policy.SideEffect)) {
				return fmt.Errorf("config: skills.%s.tool_policies side_effect %q is unsupported", name, policy.SideEffect)
			}
			if policy.RateCap < 0 {
				return fmt.Errorf("config: skills.%s.tool_policies rate_cap must be >= 0", name)
			}
		}
		if skill.Workflow != nil {
			if err := validateWorkflow(*skill.Workflow, s); err != nil {
				return fmt.Errorf("config: skills.%s.workflow: %w", name, err)
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
			candidate, ok := s.Skills[skill]
			if !ok {
				return fmt.Errorf("config: agents.%s.skills references unknown skill %q", name, skill)
			}
			if len(candidate.CompatibleAgents) > 0 && !containsString(candidate.CompatibleAgents, name) {
				return fmt.Errorf("config: agents.%s.skills references incompatible skill %q", name, skill)
			}
		}
	}
	if s.Orchestration.Planning.MaxSteps < 0 {
		return fmt.Errorf("config: orchestration.planning.max_steps must be >= 0")
	}
	if s.Orchestration.Planning.Agent != "" {
		if _, ok := s.Agents[s.Orchestration.Planning.Agent]; !ok {
			return fmt.Errorf("config: orchestration.planning.agent references unknown agent %q", s.Orchestration.Planning.Agent)
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
	if !containsString(supportedWorkflowNodeKinds, string(node.Kind)) {
		return fmt.Errorf("config: workflow node %q kind %q is unsupported", node.ID, node.Kind)
	}
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
	}
	return nil
}
