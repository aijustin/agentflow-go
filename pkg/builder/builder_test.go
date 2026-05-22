package builder_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestAutonomousScenarioMatchesYAMLShape(t *testing.T) {
	got := builder.New("autonomous-echo").
		DefaultMockLLM().
		SessionMemory().
		EchoTool().
		Agent("assistant").
		DefaultLLM().
		SessionMemory().
		EchoTool().
		Instructions("Answer the user clearly.").
		Autonomous().
		Scenario()

	if got.Name != "autonomous-echo" {
		t.Fatalf("name=%q", got.Name)
	}
	if got.Orchestration.Mode != builder.ModeAutonomous {
		t.Fatalf("mode=%q", got.Orchestration.Mode)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents=%d", len(got.Agents))
	}
	agent := got.Agents["assistant"]
	if agent.LLM != builder.NameDefaultLLM || agent.Memory != builder.NameSessionMemory || len(agent.Tools) != 1 {
		t.Fatalf("agent=%+v", agent)
	}
	if err := agentflow.ValidateScenario(got); err != nil {
		t.Fatal(err)
	}
}

func TestFixedWorkflowScenario(t *testing.T) {
	wf := builder.NewWorkflow().
		NodeTool("inspect", builder.NameRepoSearch).
		NodeAgent("review", "reviewer").
		Edge("inspect", "review").
		Build()

	got := builder.New("fixed-workflow-review").
		LLM("planner", builder.MockLLM()).
		SessionMemory().
		RepoSearchTool().
		Agent("reviewer").
		LLM("planner").
		SessionMemory().
		RepoSearchTool().
		Instructions("Review repository changes.").
		FixedWorkflow(wf).
		Scenario()

	if got.Orchestration.Mode != builder.ModeFixedWorkflow {
		t.Fatalf("mode=%q", got.Orchestration.Mode)
	}
	if got.Orchestration.Workflow == nil || len(got.Orchestration.Workflow.Nodes) != 2 {
		t.Fatalf("workflow=%+v", got.Orchestration.Workflow)
	}
	if err := agentflow.ValidateScenario(got); err != nil {
		t.Fatal(err)
	}
}

func TestAgentDoneExplicit(t *testing.T) {
	got := builder.New("explicit-done").
		DefaultMockLLM().
		Agent("a").Instructions("one").Done().
		Agent("b").Instructions("two").Done().
		Autonomous().
		Scenario()

	if len(got.Agents) != 2 {
		t.Fatalf("agents=%d", len(got.Agents))
	}
}

func TestToolBuilderChaining(t *testing.T) {
	got := builder.New("tool-chain").
		DefaultMockLLM().
		EchoTool().
		Approval(builder.ApprovalNever).
		SideEffect(builder.SideEffectRead).
		Agent("assistant").DefaultLLM().Done().
		Autonomous().
		Scenario()

	tool := got.Tools[builder.NameEchoTool]
	if tool.Type != builder.ToolTypeEcho || tool.Approval != builder.ApprovalNever || tool.SideEffect != builder.SideEffectRead {
		t.Fatalf("tool=%+v", tool)
	}
}

func TestMustScenarioPanicsWithoutAgent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	builder.New("empty").MustScenario()
}

func TestHybridWithPlanning(t *testing.T) {
	wf := builder.NewWorkflow().NodeAgent("plan", "planner").Build()
	got := builder.New("hybrid-plan").
		DefaultMockLLM().
		Agent("planner").DefaultLLM().Done().
		Hybrid(wf).
		Orchestration(builder.Planning(true, builder.PlanningExecute(true))).
		Scenario()

	if got.Orchestration.Mode != builder.ModeHybrid {
		t.Fatalf("mode=%q", got.Orchestration.Mode)
	}
	if !got.Orchestration.Planning.Enabled || !got.Orchestration.Planning.Execute {
		t.Fatalf("planning=%+v", got.Orchestration.Planning)
	}
	if err := agentflow.ValidateScenario(got); err != nil {
		t.Fatal(err)
	}
}

func TestMinimalAutonomous(t *testing.T) {
	got := builder.MinimalAutonomous("assistant",
		builder.MinimalScenarioName("autonomous-echo"),
		builder.MinimalInstructions("Answer the user clearly."),
	)
	if got.Name != "autonomous-echo" {
		t.Fatalf("name=%q", got.Name)
	}
	if err := agentflow.ValidateScenario(got); err != nil {
		t.Fatal(err)
	}
	yamlScenario, err := agentflow.LoadScenarioFile("../../examples/autonomous.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got.Orchestration.Mode != yamlScenario.Orchestration.Mode {
		t.Fatalf("mode mismatch")
	}
	if got.Tools[builder.NameEchoTool].Type != yamlScenario.Tools["echo"].Type {
		t.Fatalf("tool type mismatch")
	}
}

func TestMinimalAutonomousRepoSearch(t *testing.T) {
	got := builder.MinimalAutonomous("reviewer", builder.MinimalRepoSearch())
	if got.Tools[builder.NameRepoSearch].Type != builder.ToolTypeRepoSearch {
		t.Fatalf("tool=%+v", got.Tools[builder.NameRepoSearch])
	}
	if err := agentflow.ValidateScenario(got); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinToolPresets(t *testing.T) {
	cases := []struct {
		name       string
		toolName   string
		register   func(*builder.ScenarioBuilder)
		wire       func(*builder.AgentBuilder) *builder.AgentBuilder
		wantType   string
	}{
		{
			name:     "git",
			toolName: builder.NameGitTool,
			register: func(b *builder.ScenarioBuilder) { b.GitTool() },
			wire:     func(ab *builder.AgentBuilder) *builder.AgentBuilder { return ab.GitTool() },
			wantType: builder.ToolTypeGit,
		},
		{
			name:     "ticket",
			toolName: builder.NameTicketTool,
			register: func(b *builder.ScenarioBuilder) { b.TicketTool() },
			wire:     func(ab *builder.AgentBuilder) *builder.AgentBuilder { return ab.TicketTool() },
			wantType: builder.ToolTypeTicket,
		},
		{
			name:     "sql",
			toolName: builder.NameSQLTool,
			register: func(b *builder.ScenarioBuilder) { b.SQLTool() },
			wire:     func(ab *builder.AgentBuilder) *builder.AgentBuilder { return ab.SQLTool() },
			wantType: builder.ToolTypeSQL,
		},
		{
			name:     "filesystem",
			toolName: builder.NameFilesystemTool,
			register: func(b *builder.ScenarioBuilder) { b.FilesystemTool() },
			wire:     func(ab *builder.AgentBuilder) *builder.AgentBuilder { return ab.FilesystemTool() },
			wantType: builder.ToolTypeFilesystem,
		},
		{
			name:     "http",
			toolName: builder.NameHTTPTool,
			register: func(b *builder.ScenarioBuilder) { b.BuiltinHTTPTool() },
			wire:     func(ab *builder.AgentBuilder) *builder.AgentBuilder { return ab.BuiltinHTTPTool() },
			wantType: builder.ToolTypeHTTP,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := builder.New("tools-" + tc.name).DefaultMockLLM()
			tc.register(b)
			tc.wire(b.Agent("assistant").DefaultLLM()).Autonomous()
			got := b.Scenario()
			tool := got.Tools[tc.toolName]
			if tool.Type != tc.wantType || tool.Approval != builder.ApprovalNever {
				t.Fatalf("tool=%+v", tool)
			}
			if err := agentflow.ValidateScenario(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConstsMatchYAMLSchema(t *testing.T) {
	if builder.MemoryTypeInMemory != "in_memory" {
		t.Fatal("memory type drift")
	}
	if builder.ToolTypeEcho != "builtin.echo" {
		t.Fatal("tool type drift")
	}
	if builder.MemoryScopeSession != "session" {
		t.Fatal("memory scope drift")
	}
	if builder.ToolTypeKnowledgeRetriever != "knowledge.retriever" {
		t.Fatal("knowledge tool type drift")
	}
}

func TestAdaptiveRAGWorkflowShape(t *testing.T) {
	wf := builder.AdaptiveRAGWorkflow(builder.NameKnowledgeCollectionDocs, "assistant")
	if len(wf.Nodes) != 3 || len(wf.Edges) != 3 {
		t.Fatalf("workflow=%+v", wf)
	}
}

func TestRAGWorkflowShapes(t *testing.T) {
	corrective := builder.CorrectiveRAGWorkflow(builder.NameKnowledgeCollectionDocs, "assistant")
	if len(corrective.Nodes) != 5 || len(corrective.Edges) != 5 {
		t.Fatalf("corrective=%+v", corrective)
	}
	self := builder.SelfRAGWorkflow(builder.NameKnowledgeCollectionDocs, "assistant")
	if len(self.Nodes) != 3 || len(self.Edges) != 2 {
		t.Fatalf("self=%+v", self)
	}
	hybrid := builder.HybridResearchWorkflow(builder.NameRepoSearch)
	if len(hybrid.Nodes) != 1 {
		t.Fatalf("hybrid=%+v", hybrid)
	}
	experts := builder.MultiExpertResearchWorkflow("macro", "industry", "finance")
	if len(experts.Nodes) != 1 || experts.Nodes[0].Kind != builder.NodeParallelGroup {
		t.Fatalf("experts=%+v", experts)
	}
	review := builder.CodeReviewPipelineWorkflow(builder.NameGitTool, "security", "style")
	if len(review.Nodes) != 4 {
		t.Fatalf("review=%+v", review)
	}
	enhancements := builder.WorkflowEnhancementsWorkflow()
	if len(enhancements.Nodes) != 3 || len(enhancements.Edges) != 1 {
		t.Fatalf("enhancements=%+v", enhancements)
	}
}

func TestMultiExpertResearchPlanning(t *testing.T) {
	got := builder.MultiExpertResearch()
	if got.Orchestration.Mode != builder.ModeHybrid {
		t.Fatalf("mode=%q", got.Orchestration.Mode)
	}
	if !got.Orchestration.Planning.Enabled || !got.Orchestration.Planning.Execute || got.Orchestration.Planning.MaxSteps != 4 {
		t.Fatalf("planning=%+v", got.Orchestration.Planning)
	}
	if len(got.Agents) != 4 {
		t.Fatalf("agents=%d", len(got.Agents))
	}
}

func TestCodeReviewPipelineShape(t *testing.T) {
	got := builder.CodeReviewPipeline()
	if got.Orchestration.Mode != builder.ModeFixedWorkflow {
		t.Fatalf("mode=%q", got.Orchestration.Mode)
	}
	if got.Orchestration.MaxParallel != 2 || !got.Orchestration.HumanInLoop.Enabled {
		t.Fatalf("orchestration=%+v", got.Orchestration)
	}
	gitTool := got.Tools[builder.NameGitTool]
	if gitTool.SideEffect != builder.SideEffectRead {
		t.Fatalf("git tool=%+v", gitTool)
	}
}

func TestWorkflowEnhancementsRuntime(t *testing.T) {
	got := builder.WorkflowEnhancements()
	if got.Runtime.Secrets[builder.RuntimeSecretAPIKey] != builder.RuntimeSecretAPIKeyDevValue {
		t.Fatalf("secrets=%+v", got.Runtime.Secrets)
	}
	if got.Tools[builder.NameStatusTool].Type != builder.ToolTypeEcho {
		t.Fatalf("status tool=%+v", got.Tools[builder.NameStatusTool])
	}
}

func TestContextGovernanceLLM(t *testing.T) {
	got := builder.ContextGovernance("assistant")
	profile := got.LLMs[builder.NameDefaultLLM]
	if profile.Provider != builder.LLMProviderOpenAICompat || profile.APIKeyEnv != builder.LLMEnvRealModelAPIKey {
		t.Fatalf("profile=%+v", profile)
	}
	if profile.Context.MaxInputTokens != 220 || !profile.Context.Compression.Enabled {
		t.Fatalf("context=%+v", profile.Context)
	}
	if !profile.Thinking.Enabled || profile.Thinking.BudgetTokens != 768 {
		t.Fatalf("thinking=%+v", profile.Thinking)
	}
}

func TestToolCatalogPresets(t *testing.T) {
	cases := []struct {
		name     string
		scenario func() core.Scenario
		toolName string
		wantType string
	}{
		{"http", func() core.Scenario { return builder.MinimalHTTPTool("assistant") }, builder.NameHTTPToolStatus, builder.ToolTypeHTTP},
		{"sql", func() core.Scenario { return builder.MinimalSQLTool("assistant") }, builder.NameSQLQueryTool, builder.ToolTypeSQL},
		{"filesystem", func() core.Scenario { return builder.MinimalFilesystemTool("assistant") }, builder.NameFilesystemReadTool, builder.ToolTypeFilesystem},
		{"mcp", func() core.Scenario { return builder.MinimalMCPTool("assistant") }, builder.NameMCPSearchTool, builder.ToolTypeMCPTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.scenario()
			tool := got.Tools[tc.toolName]
			if tool.Type != tc.wantType || tool.SideEffect != builder.SideEffectRead {
				t.Fatalf("tool=%+v", tool)
			}
			if err := agentflow.ValidateScenario(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMCPToolMetadata(t *testing.T) {
	got := builder.MinimalMCPTool("assistant")
	if len(got.MCP.Servers) != 1 || got.MCP.Servers[0].Name != builder.NameMCPServerDocs {
		t.Fatalf("mcp=%+v", got.MCP)
	}
	tool := got.Tools[builder.NameMCPSearchTool]
	if tool.Metadata[builder.MCPMetadataServer] != builder.NameMCPServerDocs || tool.Metadata[builder.MCPMetadataTool] != builder.MCPToolSearch {
		t.Fatalf("metadata=%+v", tool.Metadata)
	}
}

func TestSelfRAGChatContext(t *testing.T) {
	got := builder.SelfRAG("assistant")
	profile := got.LLMs[builder.NameChatLLM]
	if profile.Context.Strategy != contextwindow.StrategySlidingWindowWithSummary {
		t.Fatalf("strategy=%q", profile.Context.Strategy)
	}
	if profile.Context.StaleToolTurns != 2 || !profile.Context.ToolSchemaPruning {
		t.Fatalf("context=%+v", profile.Context)
	}
}
