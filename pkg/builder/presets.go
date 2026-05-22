package builder

import "github.com/aijustin/agentflow-go/pkg/core"

func safeBuiltinTool(toolType string) ToolOption {
	return func(t *core.Tool) {
		t.Type = toolType
		t.Approval = ApprovalNever
	}
}

// MockLLM configures a mock provider profile for local runs and tests.
func MockLLM() LLMOption {
	return Provider(LLMProviderMock, LLMModelMockTest)
}

// SessionInMemory configures in_memory memory scoped to session.
func SessionInMemory() MemoryOption {
	return InMemoryMemory(MemoryScopeSession)
}

// EchoToolPreset configures the builtin echo tool with safe defaults.
func EchoToolPreset() ToolOption {
	return safeBuiltinTool(ToolTypeEcho)
}

// RepoSearchToolPreset configures the builtin repo_search tool with safe defaults.
func RepoSearchToolPreset() ToolOption {
	return safeBuiltinTool(ToolTypeRepoSearch)
}

// GitToolPreset configures the builtin git tool with safe defaults.
func GitToolPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeGit
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
	}
}

// TicketToolPreset configures the builtin ticket tool with safe defaults.
func TicketToolPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeTicket
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
	}
}

// SQLToolPreset configures the builtin sql tool with safe defaults.
func SQLToolPreset() ToolOption {
	return safeBuiltinTool(ToolTypeSQL)
}

// FilesystemToolPreset configures the builtin filesystem tool with safe defaults.
func FilesystemToolPreset() ToolOption {
	return safeBuiltinTool(ToolTypeFilesystem)
}

// HTTPToolPreset configures the builtin http tool with safe defaults.
func HTTPToolPreset() ToolOption {
	return safeBuiltinTool(ToolTypeHTTP)
}

// DefaultMockLLM registers the conventional default mock LLM profile.
func (b *ScenarioBuilder) DefaultMockLLM() *ScenarioBuilder {
	return b.LLM(NameDefaultLLM, MockLLM())
}

// SessionMemory registers the conventional session-scoped in_memory profile.
func (b *ScenarioBuilder) SessionMemory() *ScenarioBuilder {
	return b.Memory(NameSessionMemory, SessionInMemory())
}

// EchoTool registers the conventional echo tool declaration.
func (b *ScenarioBuilder) EchoTool() *ToolBuilder {
	return b.Tool(NameEchoTool, EchoToolPreset())
}

// RepoSearchTool registers the conventional repo_search tool declaration.
func (b *ScenarioBuilder) RepoSearchTool() *ToolBuilder {
	return b.Tool(NameRepoSearch, RepoSearchToolPreset())
}

// GitTool registers the conventional git tool declaration.
func (b *ScenarioBuilder) GitTool() *ToolBuilder {
	return b.Tool(NameGitTool, GitToolPreset())
}

// TicketTool registers the conventional ticket tool declaration.
func (b *ScenarioBuilder) TicketTool() *ToolBuilder {
	return b.Tool(NameTicketTool, TicketToolPreset())
}

// SQLTool registers the conventional sql tool declaration.
func (b *ScenarioBuilder) SQLTool() *ToolBuilder {
	return b.Tool(NameSQLTool, SQLToolPreset())
}

// FilesystemTool registers the conventional filesystem tool declaration.
func (b *ScenarioBuilder) FilesystemTool() *ToolBuilder {
	return b.Tool(NameFilesystemTool, FilesystemToolPreset())
}

// BuiltinHTTPTool registers the conventional builtin http tool declaration.
func (b *ScenarioBuilder) BuiltinHTTPTool() *ToolBuilder {
	return b.Tool(NameHTTPTool, HTTPToolPreset())
}

// DefaultLLM wires the agent to NameDefaultLLM.
func (ab *AgentBuilder) DefaultLLM() *AgentBuilder {
	return ab.LLM(NameDefaultLLM)
}

// SessionMemory wires the agent to NameSessionMemory.
func (ab *AgentBuilder) SessionMemory() *AgentBuilder {
	return ab.Memory(NameSessionMemory)
}

// EchoTool appends NameEchoTool to the agent tool list.
func (ab *AgentBuilder) EchoTool() *AgentBuilder {
	return ab.Tool(NameEchoTool)
}

// ReviewerLLM wires the agent to NameReviewerLLM.
func (ab *AgentBuilder) ReviewerLLM() *AgentBuilder {
	return ab.LLM(NameReviewerLLM)
}

// GitTool appends NameGitTool to the agent tool list.
func (ab *AgentBuilder) GitTool() *AgentBuilder {
	return ab.Tool(NameGitTool)
}

// RepoSearchTool appends NameRepoSearch to the agent tool list.
func (ab *AgentBuilder) RepoSearchTool() *AgentBuilder {
	return ab.Tool(NameRepoSearch)
}

// TicketTool appends NameTicketTool to the agent tool list.
func (ab *AgentBuilder) TicketTool() *AgentBuilder {
	return ab.Tool(NameTicketTool)
}

// SQLTool appends NameSQLTool to the agent tool list.
func (ab *AgentBuilder) SQLTool() *AgentBuilder {
	return ab.Tool(NameSQLTool)
}

// FilesystemTool appends NameFilesystemTool to the agent tool list.
func (ab *AgentBuilder) FilesystemTool() *AgentBuilder {
	return ab.Tool(NameFilesystemTool)
}

// BuiltinHTTPTool appends NameHTTPTool to the agent tool list.
func (ab *AgentBuilder) BuiltinHTTPTool() *AgentBuilder {
	return ab.Tool(NameHTTPTool)
}

// StandardStack registers DefaultMockLLM and SessionMemory.
func (b *ScenarioBuilder) StandardStack() *ScenarioBuilder {
	return b.DefaultMockLLM().SessionMemory()
}

// StandardAgent wires DefaultLLM and SessionMemory on the agent.
func (ab *AgentBuilder) StandardAgent() *AgentBuilder {
	return ab.DefaultLLM().SessionMemory()
}

// MinimalAgent wires the standard agent stack plus the given tool names.
func (b *ScenarioBuilder) MinimalAgent(name, instructions string, tools ...string) *ScenarioBuilder {
	ab := b.Agent(name).StandardAgent().Instructions(instructions)
	for _, tool := range tools {
		ab.Tool(tool)
	}
	ab.Done()
	return b
}
