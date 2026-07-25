package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ScenarioPatch is an incremental, additive edit to a scenario produced by AI
// composition (ComposeMode scenario). Unlike ApplyGraph it may introduce new
// agents and skills, but it may never overwrite parts that already exist in
// the base scenario: composed output is merged into a temporary scenario, so
// an AI-generated patch must not be able to tamper with host-registered parts.
type ScenarioPatch struct {
	Mode      string                `json:"mode,omitempty"`
	Agents    map[string]core.Agent `json:"agents,omitempty"`
	Skills    map[string]core.Skill `json:"skills,omitempty"`
	Workflow  *GraphView            `json:"workflow,omitempty"`
	Workflows map[string]GraphView  `json:"workflows,omitempty"`
}

// DeepCopyScenario returns an independent copy of base. ApplyGraph only
// shallow-copies its base and merges named workflows into the shared
// Orchestration.Workflows map, so any ephemeral apply path must deep-copy
// first to avoid mutating the caller's (live) scenario.
func DeepCopyScenario(base core.Scenario) (core.Scenario, error) {
	data, err := json.Marshal(base)
	if err != nil {
		return core.Scenario{}, fmt.Errorf("graph: deep-copy scenario: %w", err)
	}
	var out core.Scenario
	if err := json.Unmarshal(data, &out); err != nil {
		return core.Scenario{}, fmt.Errorf("graph: deep-copy scenario: %w", err)
	}
	return out, nil
}

// ApplyScenarioPatch merges patch into a deep copy of base. New agents and
// skills are added only when their name does not exist in the base scenario;
// named workflows are added under the same rule. The main workflow is
// replaced when patch.Workflow is set (topology is the one thing a patch is
// expected to redefine).
func ApplyScenarioPatch(base core.Scenario, patch ScenarioPatch) (core.Scenario, error) {
	out, err := DeepCopyScenario(base)
	if err != nil {
		return core.Scenario{}, err
	}
	for name, agent := range patch.Agents {
		name = strings.TrimSpace(name)
		if name == "" {
			return core.Scenario{}, fmt.Errorf("graph: patch agent name is required")
		}
		if _, exists := out.Agents[name]; exists {
			return core.Scenario{}, fmt.Errorf("graph: patch agent %q already exists in base scenario", name)
		}
		if agent.Name == "" {
			agent.Name = name
		}
		if out.Agents == nil {
			out.Agents = make(map[string]core.Agent, len(patch.Agents))
		}
		out.Agents[name] = agent
	}
	for name, skill := range patch.Skills {
		name = strings.TrimSpace(name)
		if name == "" {
			return core.Scenario{}, fmt.Errorf("graph: patch skill name is required")
		}
		if _, exists := out.Skills[name]; exists {
			return core.Scenario{}, fmt.Errorf("graph: patch skill %q already exists in base scenario", name)
		}
		if skill.Name == "" {
			skill.Name = name
		}
		if out.Skills == nil {
			out.Skills = make(map[string]core.Skill, len(patch.Skills))
		}
		out.Skills[name] = skill
	}
	if patch.Mode != "" {
		out.Orchestration.Mode = core.OrchestrationMode(strings.TrimSpace(patch.Mode))
	}
	if patch.Workflow != nil {
		wf, err := ImportWorkflow(*patch.Workflow)
		if err != nil {
			return core.Scenario{}, err
		}
		out.Orchestration.Workflow = &wf
	}
	for name, view := range patch.Workflows {
		name = strings.TrimSpace(name)
		if name == "" {
			return core.Scenario{}, fmt.Errorf("graph: patch workflow name is required")
		}
		if _, exists := out.Orchestration.Workflows[name]; exists {
			return core.Scenario{}, fmt.Errorf("graph: patch workflow %q already exists in base scenario", name)
		}
		wf, err := ImportWorkflow(view)
		if err != nil {
			return core.Scenario{}, fmt.Errorf("graph: patch workflow %q: %w", name, err)
		}
		if out.Orchestration.Workflows == nil {
			out.Orchestration.Workflows = make(map[string]core.Workflow, len(patch.Workflows))
		}
		out.Orchestration.Workflows[name] = wf
	}
	return out, nil
}
