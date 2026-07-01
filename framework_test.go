package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func testAutonomousScenario() core.Scenario {
	return builder.MinimalAutonomous("assistant", builder.MinimalScenarioName("autonomous-echo"))
}

func TestNewRunWithDefaults(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-1", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "hello" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkWithLLMGateway(t *testing.T) {
	scenario := testAutonomousScenario()
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "from llm"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "from llm" {
		t.Fatalf("got %q", result.Output)
	}
}

func TestFrameworkRunExecutesFixedWorkflow(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalFixedWorkflowReview("reviewer"),
		agentflow.WithToolExecutor("repo_search", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-workflow", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["inspect"]; !ok {
		t.Fatalf("expected inspect step output: %+v", snapshot.StepOutputs)
	}
	if _, ok := snapshot.StepOutputs["review"]; !ok {
		t.Fatalf("expected review step output: %+v", snapshot.StepOutputs)
	}
}

func TestFrameworkFixedWorkflowAgentNodeCallsLLM(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalFixedWorkflowReview("reviewer"),
		agentflow.WithLLMGateway(fakeGateway{content: "llm review"}),
		agentflow.WithToolExecutor("repo_search", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-agent-node", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-agent-node")
	if err != nil {
		t.Fatal(err)
	}
	var output core.AgentOutput
	if err := json.Unmarshal(snapshot.StepOutputs["review"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output.Text != "llm review" {
		t.Fatalf("expected agent node to call LLM, got %+v", output)
	}
}

func TestFrameworkWithToolResolverResolvesToolLazily(t *testing.T) {
	scenario := core.Scenario{
		Name: "lazy-tools",
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Description: "Echo input", Approval: core.ApprovalNever},
		},
		Agents: map[string]core.Agent{
			"worker": {Name: "worker"},
		},
		Orchestration: core.Orchestration{
			Mode:     core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{ID: "echo", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"hi"}`)}}},
		},
	}
	resolveCalls := 0
	resolver := agentflow.ToolResolverFunc(func(ctx context.Context, tool core.Tool) (core.ToolExecutor, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolveCalls++
		if tool.Name != "echo" || tool.Type != "builtin.echo" {
			t.Fatalf("unexpected tool metadata: %+v", tool)
		}
		return noopTool{}, nil
	})

	fw, err := agentflow.New(scenario, agentflow.WithToolResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 0 {
		t.Fatalf("resolver should not be called during framework construction, got %d", resolveCalls)
	}
	for _, runID := range []string{"run-1", "run-2"} {
		result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "worker"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != runstate.RunStatusCompleted {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if resolveCalls != 1 {
		t.Fatalf("resolver should be cached after first use, got %d calls", resolveCalls)
	}
}

func TestProviderConstructorsExposeBuiltInGateways(t *testing.T) {
	profile := llm.Profile{Name: "default", Provider: "openai-compatible", Model: "test"}
	openAI := agentflow.NewOpenAICompatibleGateway([]llm.Profile{profile}, nil)
	if !openAI.Supports("default", llm.CapChat) || !openAI.Supports("default", llm.CapToolCall) {
		t.Fatalf("openai-compatible gateway did not expose expected capabilities")
	}
	provider := agentflow.NewOpenAICompatibleProvider([]llm.Profile{{Name: "embed", Provider: "openai-compatible", Model: "test", Capabilities: []llm.Capability{llm.CapEmbed}}}, nil)
	if !provider.Supports("embed", llm.CapEmbed) {
		t.Fatalf("openai-compatible provider did not expose embedding capability")
	}
	local := agentflow.NewLocalGateway([]llm.Profile{profile}, nil)
	if !local.Supports("default", llm.CapStream) {
		t.Fatalf("local gateway should expose OpenAI-compatible streaming")
	}
	anthropic := agentflow.NewAnthropicGateway([]llm.Profile{profile}, nil)
	if !anthropic.Supports("default", llm.CapChat) ||
		!anthropic.Supports("default", llm.CapToolCall) ||
		!anthropic.Supports("default", llm.CapStructuredOutput) ||
		!anthropic.Supports("default", llm.CapStream) ||
		anthropic.Supports("default", llm.CapEmbed) {
		t.Fatalf("anthropic gateway capability set was unexpected")
	}
	router := agentflow.NewLLMRouter(map[string]llm.Gateway{"default": openAI})
	if !router.Supports("default", llm.CapStructuredOutput) {
		t.Fatalf("router did not expose routed structured output support")
	}
	providerRouter := agentflow.NewLLMProviderRouter(map[string]llm.Gateway{"embed": provider})
	if !providerRouter.Supports("embed", llm.CapEmbed) {
		t.Fatalf("provider router did not expose routed embedding support")
	}
}

func TestPostgresRunStateConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewPostgresRunStateRepository(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	if _, err := agentflow.NewPostgresRunStateRepository(nil, "one", "two"); err == nil {
		t.Fatal("expected too many table names error")
	}
}

func TestS3BlobStoreConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewS3BlobStore(agentflow.S3BlobStoreConfig{}); err == nil {
		t.Fatal("expected empty config error")
	}
}

func TestRedisLockerConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewRedisLocker(agentflow.RedisLockerConfig{}); err == nil {
		t.Fatal("expected empty config error")
	}
}

func TestRedisRunStateConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewRedisRunStateRepository(agentflow.RedisRunStateRepositoryConfig{}); err == nil {
		t.Fatal("expected empty config error")
	}
}

func TestAPIKeyConstructorsRejectInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewStaticAPIKeyAuthenticator(nil); err == nil {
		t.Fatal("expected empty static authenticator error")
	}
	if _, err := agentflow.NewAPIKeyMiddleware(agentflow.APIKeyMiddlewareConfig{}); err == nil {
		t.Fatal("expected missing authenticator error")
	}
}

func TestOIDCJWTConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewOIDCJWTAuthenticator(agentflow.OIDCJWTAuthenticatorConfig{}); err == nil {
		t.Fatal("expected missing discovery or jwks url error")
	}
}

func TestAuthorizationMiddlewareConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewAuthorizationMiddleware(agentflow.AuthorizationMiddlewareConfig{}); err == nil {
		t.Fatal("expected invalid authorization middleware config")
	}
}

func TestFrameworkSecurityOptionsRejectInvalidInputs(t *testing.T) {
	scenario := testAutonomousScenario()
	if _, err := agentflow.New(scenario, agentflow.WithSecurityPolicy(nil)); err == nil {
		t.Fatal("expected nil security policy error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithAuditSink(nil)); err == nil {
		t.Fatal("expected nil audit sink error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithSecurityPolicy(security.NewDefaultRolePolicy())); err != nil {
		t.Fatal(err)
	}
}

func TestAuditConstructorsRejectInvalidInputs(t *testing.T) {
	if agentflow.NewNoopAuditSink() == nil {
		t.Fatal("expected noop audit sink")
	}
	if agentflow.NewInMemoryAuditSink(10) == nil {
		t.Fatal("expected in-memory audit sink")
	}
	if _, err := agentflow.NewFileAuditSink(""); err == nil {
		t.Fatal("expected missing file path error")
	}
}

func TestAsyncRunHTTPHandlerConstructorRejectsInvalidInputs(t *testing.T) {
	if _, err := agentflow.NewAsyncRunHTTPHandler(agentflow.AsyncRunHTTPHandlerConfig{}); err == nil {
		t.Fatal("expected missing queue error")
	}
}

func TestFrameworkRunJobHandlerExecutesRunPayload(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.RunPayload{RunID: "run-job", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, RunID: "run-job", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-job")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run, got %+v", snapshot)
	}
}

func TestFrameworkRunJobHandlerRejectsInvalidJobs(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{}); err == nil {
		t.Fatal("expected missing framework error")
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-1", Type: "other"}); err == nil {
		t.Fatal("expected unsupported job type error")
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("expected invalid payload error")
	}
}

func TestFrameworkJobHandlerExecutesEventJob(t *testing.T) {
	scenario := core.Scenario{
		Name: "ticket",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"support": {Name: "support", LLM: "default"},
		},
		Triggers: []core.Trigger{{
			Event:      "ticket.created",
			Agent:      "support",
			PromptPath: "summary",
		}},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "handled"}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.EventPayload{Type: "ticket.created", Payload: json.RawMessage(`{"summary":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-event", Type: asyncpkg.EventJobType, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkJobHandlerExecutesResumeContinueJob(t *testing.T) {
	var tokenOut bytes.Buffer
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("secret"), &tokenOut),
		agentflow.WithLLMGateway(fakeGateway{content: "done"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-async-resume", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run, got %+v", result)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.ResumeContinuePayload{Token: result.Token, Decision: core.DecisionApprove})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{ID: "job-resume", Type: asyncpkg.ResumeContinueJobType, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-async-resume")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after resume.continue job, got %+v", snapshot)
	}
}

func TestFrameworkHITLPauseResume(t *testing.T) {
	var tokenOut bytes.Buffer
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("secret"), &tokenOut),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-hitl", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token == "" {
		t.Fatalf("unexpected pause result: %+v", result)
	}
	if tokenOut.Len() == 0 {
		t.Fatal("expected token to be written")
	}
	if err := fw.Resume(context.Background(), result.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-hitl")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning {
		t.Fatalf("unexpected resumed status %s", snapshot.Status)
	}
}

func TestFrameworkWithToolExecutorValidation(t *testing.T) {
	scenario := testAutonomousScenario()
	if _, err := agentflow.New(scenario, agentflow.WithToolExecutor("", noopTool{})); err == nil {
		t.Fatal("expected empty tool name error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithToolExecutor("echo", nil)); err == nil {
		t.Fatal("expected nil tool executor error")
	}
	if _, err := agentflow.New(
		scenario,
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	); err == nil {
		t.Fatal("expected duplicate tool registration error")
	}
}

func TestFileBackedConstructors(t *testing.T) {
	if _, err := agentflow.NewFileRunStateRepository(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := agentflow.NewFileBlobStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := agentflow.NewFileMemoryRepository(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

type noopTool struct{}

func (noopTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

func TestValidateScenario(t *testing.T) {
	err := agentflow.ValidateScenario(core.Scenario{Name: "missing-agents"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// TestNewRejectsAutonomousScenarioWithDuplicateSkillNodeIDs verifies that
// New() catches a malformed workflow synthesized by skill expansion (an
// agent listing the same skill twice namespaces two skill workflows under
// the identical "agent.skill." prefix, producing duplicate node ids) even
// though the scenario's orchestration mode is autonomous, which normally
// has no orchestration.workflow of its own.
func TestNewRejectsAutonomousScenarioWithDuplicateSkillNodeIDs(t *testing.T) {
	scenario := core.Scenario{
		Name:   "autonomous-dup-skill",
		LLMs:   map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Tools:  map[string]core.Tool{"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever}},
		Skills: map[string]core.Skill{"review": {Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{ID: "inspect", Kind: core.NodeTool, Ref: "echo"}}}}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Skills: []string{"review", "review"}},
		},
	}
	if _, err := agentflow.New(scenario); err == nil {
		t.Fatal("expected duplicate skill node ids to be rejected")
	}
}

type contextCaptureGateway struct {
	fakeGateway
	lastReq llm.ChatRequest
}

func (g *contextCaptureGateway) Chat(ctx context.Context, profile string, req llm.ChatRequest) (llm.ChatResponse, error) {
	g.lastReq = req
	return g.fakeGateway.Chat(ctx, profile, req)
}

func TestFrameworkHybridHydratesWorkflowContext(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-hydrate",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Agents: map[string]core.Agent{
			"analyst": {Name: "analyst", LLM: "default", Instructions: "analyze"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"workflow-data"}`)},
				},
			},
		},
	}
	gateway := &contextCaptureGateway{fakeGateway: fakeGateway{content: "hybrid-answer"}}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-hybrid", Agent: "analyst", Prompt: "summarize"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
	foundContext := false
	for _, msg := range gateway.lastReq.Messages {
		if strings.Contains(msg.Content, `"steps"`) && strings.Contains(msg.Content, `"prep"`) {
			foundContext = true
		}
	}
	if !foundContext {
		t.Fatalf("expected hydrated workflow context in LLM messages: %+v", gateway.lastReq.Messages)
	}
}

func TestFrameworkHybridResumeWithNullInputContext(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-null-input",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "approve", Kind: core.NodeHumanGate},
					{ID: "done", Kind: core.NodeTransform, DependsOn: []string{"approve"}, Input: json.RawMessage(`{"set":{"ok":true}}`)},
				},
			},
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithHITLTokenSecret([]byte("secret"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "hybrid answer"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:   "run-hybrid-null",
		Agent:   "assistant",
		Prompt:  "review",
		Context: json.RawMessage("null"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused workflow, got %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-hybrid-null")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(snapshot.Variables["input"]); got != "null" {
		t.Fatalf("expected persisted null input, got %q", snapshot.Variables["input"])
	}
	result, err = fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "hybrid answer" {
		t.Fatalf("unexpected continue result: %+v", result)
	}
}

func TestFrameworkRunStructuredAfterFixedWorkflow(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-workflow",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {
				Name: "assistant",
				LLM:  "default",
				Policy: core.AgentPolicy{
					OutputSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
				},
			},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"prep"}`)},
					{ID: "finish", Kind: core.NodeAgent, Ref: "assistant", DependsOn: []string{"prep"}},
				},
			},
		},
	}
	gateway := structuredFakeGateway{payload: json.RawMessage(`{"answer":"ok"}`)}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID:  "run-structured",
		Agent:  "assistant",
		Prompt: "summarize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"ok"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-structured")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["prep"]; !ok {
		t.Fatal("workflow step output should be preserved")
	}
}

type structuredFakeGateway struct {
	payload json.RawMessage
}

func (g structuredFakeGateway) Supports(string, llm.Capability) bool { return true }

func (g structuredFakeGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Content: string(g.payload)}}, nil
}

func (g structuredFakeGateway) StructuredChat(context.Context, string, json.RawMessage, llm.ChatRequest) (json.RawMessage, error) {
	return g.payload, nil
}

type fakeGateway struct {
	content string
}

func (f fakeGateway) Supports(string, llm.Capability) bool {
	return true
}

func (f fakeGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: f.content}}, nil
}

// ChatWithTools makes fakeGateway satisfy llm.ToolCaller so agents built
// with a default tool attached (see the builder package's minimal helpers)
// can still run against this fake without runtime.answer refusing to call
// an LLM that does not genuinely support tool calling. None of the tests
// using fakeGateway exercise an actual tool call, so this simply returns
// the same plain assistant message as Chat with no tool calls requested.
func (f fakeGateway) ChatWithTools(context.Context, string, llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: f.content}}}, nil
}

func ExampleNew() {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}))
	if err != nil {
		panic(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:  "example-run",
		Prompt: "hello",
	})
	if err != nil {
		panic(err)
	}
	out, _ := json.Marshal(result.Status)
	println(string(out))
}
