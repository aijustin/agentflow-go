package builder

import "github.com/aijustin/agentflow-go/pkg/core"

type minimalTool int

const (
	minimalToolEcho minimalTool = iota
	minimalToolRepoSearch
	minimalToolTicket
	minimalToolKnowledge
)

// MinimalOption configures MinimalAutonomous.
type MinimalOption func(*minimalConfig)

type minimalConfig struct {
	scenarioName string
	instructions string
	tool         minimalTool
}

func defaultMinimalConfig(agentName string) minimalConfig {
	return minimalConfig{
		scenarioName: "autonomous-" + agentName,
		instructions: "Answer the user clearly.",
		tool:         minimalToolEcho,
	}
}

// MinimalScenarioName sets the scenario name.
func MinimalScenarioName(name string) MinimalOption {
	return func(c *minimalConfig) {
		c.scenarioName = name
	}
}

// MinimalInstructions sets agent instructions.
func MinimalInstructions(text string) MinimalOption {
	return func(c *minimalConfig) {
		c.instructions = text
	}
}

// MinimalEcho keeps echo as the only tool. This is the default.
func MinimalEcho() MinimalOption {
	return func(c *minimalConfig) {
		c.tool = minimalToolEcho
	}
}

// MinimalRepoSearch uses repo_search instead of echo.
func MinimalRepoSearch() MinimalOption {
	return func(c *minimalConfig) {
		c.tool = minimalToolRepoSearch
	}
}

// MinimalAutonomous builds the common mock LLM + session memory + one tool +
// single-agent autonomous stack used across examples.
func MinimalAutonomous(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	for _, opt := range opts {
		opt(&cfg)
	}

	b := New(cfg.scenarioName).StandardStack()
	ab := b.Agent(agentName).StandardAgent().Instructions(cfg.instructions)
	switch cfg.tool {
	case minimalToolRepoSearch:
		b.RepoSearchTool()
		ab.RepoSearchTool()
	default:
		b.EchoTool()
		ab.EchoTool()
	}
	return ab.Autonomous().Scenario()
}

// NewMinimal starts a scenario with the standard mock/session stack. Register
// tools, then call MinimalAgent or configure agents manually.
func NewMinimal(scenarioName string) *ScenarioBuilder {
	return New(scenarioName).StandardStack()
}
