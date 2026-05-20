package scenario

import (
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type RuntimePlan struct {
	Scenario core.Scenario
	LLMs     map[string]llm.Profile
	Memory   map[string]memory.Namespace
}

func Build(s core.Scenario) (RuntimePlan, error) {
	expanded, err := expandSkills(s)
	if err != nil {
		return RuntimePlan{}, err
	}
	expanded, err = expandWorkflowSkillNodes(expanded)
	if err != nil {
		return RuntimePlan{}, err
	}
	if err := validateNoSkillNodes(expanded); err != nil {
		return RuntimePlan{}, err
	}
	plan := RuntimePlan{
		Scenario: expanded,
		LLMs:     make(map[string]llm.Profile, len(s.LLMs)),
		Memory:   make(map[string]memory.Namespace, len(s.Memories)),
	}
	for name, profile := range s.LLMs {
		plan.LLMs[name] = llm.Profile{
			Name:                name,
			Provider:            profile.Provider,
			Model:               profile.Model,
			Endpoint:            profile.Endpoint,
			APIKeyEnv:           profile.APIKeyEnv,
			ContextWindowTokens: profile.ContextWindowTokens,
			MaxOutputTokens:     profile.MaxOutputTokens,
			Temperature:         profile.Temperature,
			TopP:                profile.TopP,
			Timeout:             profile.Timeout,
			Thinking:            profile.Thinking,
			ReasoningEffort:     profile.ReasoningEffort,
			Context:             profile.Context,
			ExtraBody:           profile.ExtraBody,
			Capabilities:        llm.CapabilitiesFromStrings(profile.Capabilities),
			Metadata:            profile.Metadata,
		}
	}
	for name, mem := range s.Memories {
		plan.Memory[name] = memory.Namespace{
			Scope: memory.Scope(mem.Scope),
		}
	}
	if len(plan.Scenario.Agents) == 0 {
		return RuntimePlan{}, fmt.Errorf("scenario: at least one agent is required")
	}
	return plan, nil
}

func expandSkills(s core.Scenario) (core.Scenario, error) {
	if len(s.Agents) == 0 {
		return s, nil
	}
	if s.Agents != nil {
		agents := make(map[string]core.Agent, len(s.Agents))
		for name, agent := range s.Agents {
			for _, skillName := range agent.Skills {
				skill, ok := s.Skills[skillName]
				if !ok {
					return core.Scenario{}, fmt.Errorf("scenario: agent %q references unknown skill %q", name, skillName)
				}
				if len(skill.CompatibleAgents) > 0 && !contains(skill.CompatibleAgents, name) {
					return core.Scenario{}, fmt.Errorf("scenario: agent %q references incompatible skill %q", name, skillName)
				}
				agent.Instructions = mergePromptFragments(agent.Instructions, skill.PromptFragments)
				agent.Policy = mergeAgentPolicy(agent.Policy, skill.AgentPolicy)
				applySkillToolPolicies(s.Tools, skill.ToolPolicies)
			}
			agents[name] = agent
		}
		s.Agents = agents
	}

	var extraNodes []core.WorkflowNode
	var extraEdges []core.WorkflowEdge
	for agentName, agent := range s.Agents {
		for _, skillName := range agent.Skills {
			skill := s.Skills[skillName]
			if skill.Workflow == nil {
				continue
			}
			prefix := agentName + "." + skillName + "."
			for _, node := range skill.Workflow.Nodes {
				node.ID = prefix + node.ID
				node.DependsOn = namespaceIDs(prefix, node.DependsOn)
				extraNodes = append(extraNodes, node)
			}
			for _, edge := range skill.Workflow.Edges {
				edge.From = prefix + edge.From
				edge.To = prefix + edge.To
				extraEdges = append(extraEdges, edge)
			}
		}
	}
	if len(extraNodes) == 0 {
		return s, nil
	}
	if s.Orchestration.Workflow == nil {
		s.Orchestration.Workflow = &core.Workflow{}
	}
	s.Orchestration.Workflow.Nodes = append(s.Orchestration.Workflow.Nodes, extraNodes...)
	s.Orchestration.Workflow.Edges = append(s.Orchestration.Workflow.Edges, extraEdges...)
	return s, nil
}

func expandWorkflowSkillNodes(s core.Scenario) (core.Scenario, error) {
	if s.Orchestration.Workflow == nil {
		return s, nil
	}
	workflow := *s.Orchestration.Workflow
	replacements := make(map[string]skillExpansion)
	var kept []core.WorkflowNode
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeSkill {
			kept = append(kept, node)
			continue
		}
		skill, ok := s.Skills[node.Ref]
		if !ok {
			return core.Scenario{}, fmt.Errorf("scenario: workflow node %q references unknown skill %q", node.ID, node.Ref)
		}
		if skill.Workflow == nil {
			return core.Scenario{}, fmt.Errorf("scenario: workflow node %q skill %q has no workflow", node.ID, node.Ref)
		}
		prefix := node.ID + "."
		entries := namespaceIDs(prefix, skillEntryNodes(*skill.Workflow))
		exits := namespaceIDs(prefix, skillExitNodes(*skill.Workflow))
		replacements[node.ID] = skillExpansion{entries: entries, exits: exits}
		entrySet := make(map[string]bool, len(entries))
		for _, entry := range entries {
			entrySet[entry] = true
		}
		for _, skillNode := range skill.Workflow.Nodes {
			skillNode.ID = prefix + skillNode.ID
			skillNode.DependsOn = namespaceIDs(prefix, skillNode.DependsOn)
			if entrySet[skillNode.ID] {
				skillNode.DependsOn = append(append([]string(nil), node.DependsOn...), skillNode.DependsOn...)
			}
			kept = append(kept, skillNode)
		}
		for _, edge := range skill.Workflow.Edges {
			workflow.Edges = append(workflow.Edges, core.WorkflowEdge{
				From:      prefix + edge.From,
				To:        prefix + edge.To,
				Condition: edge.Condition,
			})
		}
	}
	for index, node := range kept {
		kept[index] = rewireSkillDependencies(node, replacements)
	}
	var edges []core.WorkflowEdge
	for _, edge := range workflow.Edges {
		if replacement, ok := replacements[edge.From]; ok {
			for _, entry := range replacement.entries {
				edges = append(edges, core.WorkflowEdge{From: entry, To: edge.To, Condition: edge.Condition})
			}
			continue
		}
		if replacement, ok := replacements[edge.To]; ok {
			for _, exitID := range replacement.exits {
				edges = append(edges, core.WorkflowEdge{From: edge.From, To: exitID, Condition: edge.Condition})
			}
			continue
		}
		edges = append(edges, edge)
	}
	workflow.Nodes = kept
	workflow.Edges = edges
	s.Orchestration.Workflow = &workflow
	return s, nil
}

type skillExpansion struct {
	entries []string
	exits   []string
}

func rewireSkillDependencies(node core.WorkflowNode, replacements map[string]skillExpansion) core.WorkflowNode {
	if len(node.DependsOn) == 0 {
		return node
	}
	depends := make([]string, 0, len(node.DependsOn))
	for _, dep := range node.DependsOn {
		if replacement, ok := replacements[dep]; ok {
			depends = append(depends, replacement.exits...)
			continue
		}
		depends = append(depends, dep)
	}
	node.DependsOn = depends
	return node
}

func skillEntryNodes(workflow core.Workflow) []string {
	incoming := make(map[string]bool)
	for _, edge := range workflow.Edges {
		incoming[edge.To] = true
	}
	entries := make([]string, 0)
	for _, node := range workflow.Nodes {
		if len(node.DependsOn) == 0 && !incoming[node.ID] {
			entries = append(entries, node.ID)
		}
	}
	if len(entries) == 0 && len(workflow.Nodes) > 0 {
		entries = append(entries, workflow.Nodes[0].ID)
	}
	return entries
}

func skillExitNodes(workflow core.Workflow) []string {
	outgoing := make(map[string]bool)
	for _, edge := range workflow.Edges {
		outgoing[edge.From] = true
	}
	exits := make([]string, 0)
	for _, node := range workflow.Nodes {
		if !outgoing[node.ID] {
			exits = append(exits, node.ID)
		}
	}
	if len(exits) == 0 && len(workflow.Nodes) > 0 {
		exits = append(exits, workflow.Nodes[len(workflow.Nodes)-1].ID)
	}
	return exits
}

func applySkillToolPolicies(tools map[string]core.Tool, policies []core.SkillToolPolicy) {
	if len(policies) == 0 || len(tools) == 0 {
		return
	}
	for _, policy := range policies {
		tool, ok := tools[policy.Tool]
		if !ok {
			continue
		}
		if policy.Approval != "" {
			tool.Approval = policy.Approval
		}
		if policy.SideEffect != "" {
			tool.SideEffect = policy.SideEffect
		}
		if policy.RateCap > 0 {
			tool.RateCap = policy.RateCap
		}
		tools[policy.Tool] = tool
	}
}

func mergeAgentPolicy(base core.AgentPolicy, overlay core.AgentPolicy) core.AgentPolicy {
	if overlay.MaxSteps > 0 {
		base.MaxSteps = overlay.MaxSteps
	}
	if overlay.Timeout > 0 {
		base.Timeout = overlay.Timeout
	}
	if overlay.RetryLimit > 0 {
		base.RetryLimit = overlay.RetryLimit
	}
	if len(overlay.OutputSchema) > 0 {
		base.OutputSchema = append(base.OutputSchema[:0:0], overlay.OutputSchema...)
	}
	if len(overlay.HumanCheckpoints) > 0 {
		base.HumanCheckpoints = append([]string(nil), overlay.HumanCheckpoints...)
	}
	return base
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func namespaceIDs(prefix string, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, prefix+id)
	}
	return out
}

func mergePromptFragments(instructions string, fragments []core.PromptFragment) string {
	if len(fragments) == 0 {
		return instructions
	}
	var b strings.Builder
	b.WriteString(instructions)
	for _, fragment := range fragments {
		if fragment.Content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if fragment.Name != "" {
			b.WriteString("[")
			b.WriteString(fragment.Name)
			b.WriteString("]\n")
		}
		b.WriteString(fragment.Content)
	}
	return b.String()
}

// validateNoSkillNodes ensures that after expandSkills there are no residual
// NodeSkill nodes in the workflow.  A NodeSkill node that survives expansion
// means a skill was referenced in a workflow node directly (rather than via an
// agent), which is unsupported.
func validateNoSkillNodes(s core.Scenario) error {
	if s.Orchestration.Workflow == nil {
		return nil
	}
	for _, node := range s.Orchestration.Workflow.Nodes {
		if node.Kind == core.NodeSkill {
			return fmt.Errorf("scenario: workflow node %q has kind %q which must be expanded before runtime; reference skills via agent.skills instead", node.ID, node.Kind)
		}
	}
	return nil
}
