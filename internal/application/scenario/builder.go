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
				agent.Instructions = mergePromptFragments(agent.Instructions, skill.PromptFragments)
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
		if policy.RateCap > 0 {
			tool.RateCap = policy.RateCap
		}
		tools[policy.Tool] = tool
	}
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
