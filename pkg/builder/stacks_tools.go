package builder

import "github.com/aijustin/agentflow-go/pkg/core"

func minimalToolAgentScenario(
	scenarioName, agentName, instructions string,
	register func(*ScenarioBuilder),
	wire func(*AgentBuilder) *AgentBuilder,
) core.Scenario {
	b := New(scenarioName).ToolAssistantStack()
	register(b)
	ab := wire(b.Agent(agentName).DefaultLLM().Instructions(instructions))
	return ab.Autonomous().Scenario()
}

// MinimalHTTPTool builds the http tool example for catalog ID http-tool.
func MinimalHTTPTool(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "http-tool-example"
	cfg.instructions = "Use http.status only for allowlisted service status checks."
	for _, opt := range opts {
		opt(&cfg)
	}
	return minimalToolAgentScenario(
		cfg.scenarioName,
		agentName,
		cfg.instructions,
		func(b *ScenarioBuilder) { b.HTTPToolStatus() },
		func(ab *AgentBuilder) *AgentBuilder { return ab.HTTPToolStatus() },
	)
}

// MinimalSQLTool builds the sql tool example for catalog ID sql-tool.
func MinimalSQLTool(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "sql-tool-example"
	cfg.instructions = "Use sql.query only for allowlisted read-only lookups."
	for _, opt := range opts {
		opt(&cfg)
	}
	return minimalToolAgentScenario(
		cfg.scenarioName,
		agentName,
		cfg.instructions,
		func(b *ScenarioBuilder) { b.SQLQueryTool() },
		func(ab *AgentBuilder) *AgentBuilder { return ab.SQLQueryTool() },
	)
}

// MinimalFilesystemTool builds the filesystem tool example for catalog ID filesystem-tool.
func MinimalFilesystemTool(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "filesystem-tool-example"
	cfg.instructions = "Use fs.read only for allowlisted local runbooks or documentation."
	for _, opt := range opts {
		opt(&cfg)
	}
	return minimalToolAgentScenario(
		cfg.scenarioName,
		agentName,
		cfg.instructions,
		func(b *ScenarioBuilder) { b.FilesystemReadTool() },
		func(ab *AgentBuilder) *AgentBuilder { return ab.FilesystemReadTool() },
	)
}

// MinimalMCPTool builds the MCP tool example for catalog ID mcp-tool.
func MinimalMCPTool(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "mcp-tool-example"
	cfg.instructions = "Use docs.search when the answer depends on company documentation."
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		ToolAssistantStack().
		DocsMCPServer().
		MCPSearchTool()
	ab := b.Agent(agentName).
		DefaultLLM().
		MCPSearchTool().
		Instructions(cfg.instructions)
	return ab.Autonomous().Scenario()
}
