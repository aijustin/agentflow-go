package catalog

import (
	"sort"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type ToolEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Approval   string `json:"approval,omitempty"`
	SideEffect string `json:"side_effect,omitempty"`
}

type SkillEntry struct {
	Name             string   `json:"name"`
	CompatibleAgents []string `json:"compatible_agents,omitempty"`
	HasWorkflow      bool     `json:"has_workflow"`
}

type AgentEntry struct {
	Name   string   `json:"name"`
	LLM    string   `json:"llm,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Skills []string `json:"skills,omitempty"`
}

type Catalog struct {
	Tools  []ToolEntry  `json:"tools"`
	Skills []SkillEntry `json:"skills"`
	Agents []AgentEntry `json:"agents"`
}

func FromScenario(scenario core.Scenario) Catalog {
	tools := make([]ToolEntry, 0, len(scenario.Tools))
	for name, tool := range scenario.Tools {
		tools = append(tools, ToolEntry{
			Name:       name,
			Type:       tool.Type,
			Approval:   string(tool.Approval),
			SideEffect: string(tool.SideEffect),
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	skills := make([]SkillEntry, 0, len(scenario.Skills))
	for name, skill := range scenario.Skills {
		skills = append(skills, SkillEntry{
			Name:             name,
			CompatibleAgents: append([]string(nil), skill.CompatibleAgents...),
			HasWorkflow:      skill.Workflow != nil,
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })

	agents := make([]AgentEntry, 0, len(scenario.Agents))
	for name, agent := range scenario.Agents {
		agents = append(agents, AgentEntry{
			Name:   name,
			LLM:    agent.LLM,
			Tools:  append([]string(nil), agent.Tools...),
			Skills: append([]string(nil), agent.Skills...),
		})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })

	return Catalog{Tools: tools, Skills: skills, Agents: agents}
}
