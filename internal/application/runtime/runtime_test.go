package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestNewEngineRequiresRunStateRepository(t *testing.T) {
	_, err := NewEngine(core.Scenario{}, Dependencies{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEngineRunEchoesPromptWithoutLLM(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "hello" {
		t.Fatalf("unexpected result: %+v", result)
	}
	loaded, err := repo.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCompleted {
		t.Fatalf("snapshot not completed: %+v", loaded)
	}
}

func TestEngineRunUsesLLM(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "llm answer"}})
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "llm answer" {
		t.Fatalf("got %q", result.Output)
	}
}

func TestEngineRunAppliesContextGovernance(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := &capturingGateway{response: "governed"}
	scenario := baseScenario(false)
	temp := float32(0.1)
	scenario.LLMs = map[string]core.LLMProfileRef{
		"default": {
			MaxOutputTokens: 64,
			Temperature:     &temp,
			Context: contextwindow.Policy{
				Strategy:               contextwindow.StrategySlidingWindowWithSummary,
				MaxInputTokens:         80,
				SummaryTokens:          40,
				SystemPromptProtection: true,
			},
		},
	}

	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{
		RunID:   "run-1",
		Agent:   "assistant",
		Prompt:  "final question",
		Context: []byte(`{"history":"` + strings.Repeat("very long context ", 120) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateway.req.MaxTokens != 64 || gateway.req.Temperature == nil || *gateway.req.Temperature != temp {
		t.Fatalf("llm request config not propagated: %+v", gateway.req)
	}
	foundSummary := false
	for _, msg := range gateway.req.Messages {
		if strings.Contains(msg.Content, "Earlier context summary") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected summarized context messages: %+v", gateway.req.Messages)
	}
}

func TestEngineRunReadsAndWritesMemory(t *testing.T) {
	ctx := context.Background()
	memRepo := memoryinmem.NewRepository()
	ns := memory.Namespace{SessionID: "test-session", Agent: "assistant", Scope: memory.ScopeSession}
	prior, err := json.Marshal(memoryMessage{Role: string(llm.RoleAssistant), Content: "prior answer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := memRepo.Append(ctx, ns, "messages", prior); err != nil {
		t.Fatal(err)
	}
	gateway := &capturingGateway{response: "new answer"}
	events := &captureEvents{}
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"session": {Type: "in_memory", Scope: string(memory.ScopeSession), Namespace: "test-session"},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Memory: map[string]memory.Repository{"session": memRepo},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(ctx, RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "new question"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "new answer" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	foundPrior := false
	for _, msg := range gateway.req.Messages {
		if msg.Content == "prior answer" {
			foundPrior = true
		}
	}
	if !foundPrior {
		t.Fatalf("memory was not injected into llm messages: %+v", gateway.req.Messages)
	}
	raw, err := memRepo.Get(ctx, ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[1].Content != "new question" || stored[2].Content != "new answer" {
		t.Fatalf("memory was not appended correctly: %+v", stored)
	}
	if !events.has(core.EventMemoryRead) || !events.has(core.EventMemoryWrite) {
		t.Fatalf("expected memory events, got %+v", events.types())
	}
}

func TestEngineRunAutonomousToolLoopExecutesTool(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   repo,
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final answer" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	requests := gateway.ToolRequests("default")
	if len(requests) != 2 || len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "echo" {
		t.Fatalf("tool specs not passed to llm: %+v", requests)
	}
	if !events.has(core.EventToolCalled) || !events.has(core.EventToolReturned) || !events.has(core.EventLLMCalled) {
		t.Fatalf("expected tool and llm events, got %+v", events.types())
	}
	loaded, err := repo.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["tool.call-1"]; !ok {
		t.Fatalf("tool output not persisted: %+v", loaded.StepOutputs)
	}
}

func TestEngineRunAutonomousPlanningInjectsPlanIntoToolLoop(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"steps":[{"goal":"call echo before answering","tool":"echo"}]}`))
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true, MaxSteps: 3}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final answer" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	requests := gateway.ToolRequests("default")
	if len(requests) == 0 {
		t.Fatal("expected tool request")
	}
	foundPlan := false
	for _, message := range requests[0].Messages {
		if strings.Contains(message.Content, "call echo before answering") {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("plan was not injected into tool loop messages: %+v", requests[0].Messages)
	}
}

func TestEngineRunAuthorizesToolInvocationAndAudits(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
	})
	principal := identity.Principal{ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleService}}
	audits := &captureAudit{}
	policyCalls := 0
	policy := security.PolicyFunc(func(ctx context.Context, got identity.Principal, action security.Action, resource security.Resource) error {
		policyCalls++
		if got.ID != principal.ID || action != security.ActionToolInvoke || resource.Type != "tool" || resource.ID != "echo" {
			t.Fatalf("unexpected policy input principal=%+v action=%s resource=%+v", got, action, resource)
		}
		return nil
	})
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Policy: policy,
		Audit:  audits,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), principal)
	result, err := engine.Run(ctx, RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final answer" || policyCalls != 1 {
		t.Fatalf("unexpected result output=%q policyCalls=%d", result.Output, policyCalls)
	}
	if !audits.has(audit.EventToolInvoked) {
		t.Fatalf("expected tool invoked audit event, got %+v", audits.events)
	}
}

func TestEngineRunDeniesToolInvocationByPolicyAndAudits(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "denied handled"}},
	})
	principal := identity.Principal{ID: "viewer-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}}
	audits := &captureAudit{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": failingTool{}},
		Policy: security.NewDefaultRolePolicy(),
		Audit:  audits,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), principal)
	result, err := engine.Run(ctx, RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "denied handled" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if !audits.has(audit.EventPolicyDenied) || audits.has(audit.EventToolInvoked) {
		t.Fatalf("expected policy denied audit without tool invoked, got %+v", audits.events)
	}
}

func TestEngineRunDeniesToolInvocationByGovernance(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "blocked handled"}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectDangerous, 4), Dependencies{
		Runs:       runstateinmem.NewRepository(),
		LLM:        gateway,
		Tools:      mapToolRegistry{"echo": failingTool{}},
		Events:     events,
		ToolPolicy: governance.NewMaxSideEffectPolicy(core.SideEffectRead),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "blocked handled" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if !events.has(core.EventToolDenied) || events.has(core.EventToolCalled) {
		t.Fatalf("expected governance denial without execution, got %+v", events.types())
	}
}

func TestEngineRunAutonomousToolLoopDeniesApprovalRequiredTool(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "approval required"}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalAlways, core.SideEffectExternal, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": failingTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "approval required" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if !events.has(core.EventToolDenied) || events.has(core.EventToolCalled) {
		t.Fatalf("expected denied without execution, got %+v", events.types())
	}
}

func TestEngineRunAutonomousToolLoopHonorsMaxSteps(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 1), Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "loop"})
	if err == nil || !strings.Contains(err.Error(), "max_steps=1") {
		t.Fatalf("expected max_steps error, got %v", err)
	}
}

func TestEngineRunAppliesRuntimeTimeoutAndMarksFailed(t *testing.T) {
	repo := runstateinmem.NewRepository()
	scenario := baseScenario(false)
	scenario.Runtime.Timeout = time.Millisecond
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: blockingGateway{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-timeout", Agent: "assistant", Prompt: "wait"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	loaded, err := repo.Load(context.Background(), "run-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed snapshot, got %+v", loaded)
	}
}

func TestEngineRunRetriesLLMChat(t *testing.T) {
	gateway := &retryGateway{failures: 1}
	scenario := baseScenario(false)
	scenario.Runtime.MaxRetries = 1
	engine, err := NewEngine(scenario, Dependencies{Runs: runstateinmem.NewRepository(), LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-retry", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "retried" || gateway.calls != 2 {
		t.Fatalf("retry did not run as expected: output=%q calls=%d", result.Output, gateway.calls)
	}
}

func TestEngineRunDoesNotRetryPermanentLLMError(t *testing.T) {
	gateway := &retryGateway{failures: 3, err: permanentRetryError{message: "bad request"}}
	scenario := baseScenario(false)
	scenario.Runtime.MaxRetries = 3
	engine, err := NewEngine(scenario, Dependencies{Runs: runstateinmem.NewRepository(), LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-permanent", Agent: "assistant", Prompt: "hello"})
	if err == nil {
		t.Fatal("expected permanent error")
	}
	if gateway.calls != 1 {
		t.Fatalf("permanent error should not be retried, got %d calls", gateway.calls)
	}
}

func TestEngineRunRetriesToolExecution(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Runtime.MaxRetries = 1
	tool := &flakyTool{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-tool-retry", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	if tool.calls != 2 {
		t.Fatalf("expected tool retry, got %d calls", tool.calls)
	}
}

func TestEngineRunDeniesToolAfterRateCap(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"first"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-2", Name: "echo", Input: json.RawMessage(`{"query":"second"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	events := &captureEvents{}
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	tool := scenario.Tools["echo"]
	tool.RateCap = 1
	scenario.Tools["echo"] = tool
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-rate", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if events.count(core.EventToolCalled) != 1 || events.count(core.EventToolDenied) != 1 {
		t.Fatalf("expected one tool call and one denial, got %+v", events.types())
	}
}

func TestEngineRunCompactsLargeToolResultBeforeNextLLMCall(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	profile := scenario.LLMs["default"]
	profile.Context.ToolResultMaxTokens = 20
	scenario.LLMs = map[string]core.LLMProfileRef{"default": profile}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": largeOutputTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-compact", Agent: "assistant", Prompt: "use echo"}); err != nil {
		t.Fatal(err)
	}
	requests := gateway.ToolRequests("default")
	if len(requests) != 2 {
		t.Fatalf("expected two tool loop requests, got %+v", requests)
	}
	messages := requests[1].Messages
	if len(messages) == 0 {
		t.Fatal("expected messages in second request")
	}
	content := messages[len(messages)-1].Content
	if !strings.Contains(content, `"truncated":true`) || strings.Contains(content, strings.Repeat("x", 256)) {
		t.Fatalf("tool result was not compacted before context reuse: %s", content)
	}
}

func TestEngineRunExternalizesLargeFinalOutput(t *testing.T) {
	repo := runstateinmem.NewRepository()
	blobs := blobinmem.NewStore()
	scenario := baseScenario(false)
	scenario.Runtime.StepOutputThreshold = 16
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-blob", Prompt: strings.Repeat("x", 64)}); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(context.Background(), "run-blob")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StepOutputs["final"].Blob == nil {
		t.Fatalf("expected final output blob ref, got %+v", loaded.StepOutputs["final"])
	}
}

func TestEngineRunRedactsPersistedFinalOutput(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, OutputRedactor: governance.NewJSONFieldRedactor("text")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-redact", Prompt: "classified"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "classified" {
		t.Fatalf("run result should keep in-process output, got %q", result.Output)
	}
	loaded, err := repo.Load(context.Background(), "run-redact")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(loaded.StepOutputs["final"].Inline), "classified") || !strings.Contains(string(loaded.StepOutputs["final"].Inline), "[REDACTED]") {
		t.Fatalf("final output was not redacted before persistence: %s", loaded.StepOutputs["final"].Inline)
	}
}

func TestEngineRunStructuredUsesAgentOutputSchema(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"answer":"ok"}`))
	scenario := baseScenario(false)
	agent := scenario.Agents["assistant"]
	agent.Policy.OutputSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.RunStructured(context.Background(), RunRequest{RunID: "run-structured", Agent: "assistant", Prompt: "json"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.StructuredOutput) != `{"answer":"ok"}` || result.Output != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured result: %+v", result)
	}
	loaded, err := repo.Load(context.Background(), "run-structured")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.StepOutputs["final"].Inline) != `{"answer":"ok"}` {
		t.Fatalf("structured output not persisted directly: %+v", loaded.StepOutputs["final"])
	}
}

func TestEngineStreamCompletesRunAndWritesMemory(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	memRepo := memoryinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapStream)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "streamed"}})
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{"session": {Type: "in_memory", Scope: string(memory.ScopeSession), Namespace: "stream-session"}}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   repo,
		LLM:    gateway,
		Memory: map[string]memory.Repository{"session": memRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(ctx, RunRequest{RunID: "run-stream", Agent: "assistant", Prompt: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for chunk := range ch {
		if chunk.Error != "" {
			t.Fatal(chunk.Error)
		}
		got += chunk.Content
	}
	if got != "streamed" {
		t.Fatalf("unexpected streamed content %q", got)
	}
	loaded, err := repo.Load(ctx, "run-stream")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCompleted {
		t.Fatalf("run not completed: %+v", loaded)
	}
	raw, err := memRepo.Get(ctx, memory.Namespace{SessionID: "stream-session", Agent: "assistant", Scope: memory.ScopeSession}, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "streamed") {
		t.Fatalf("stream output not written to memory: %s", raw)
	}
}

func TestEngineStreamSupportsAutonomousToolLoop(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStream, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"steps":[{"goal":"stream echo plan","tool":"echo"}]}`))
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{RunID: "run-stream-tools", Agent: "assistant", Prompt: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	done := false
	for chunk := range ch {
		if chunk.Error != "" {
			t.Fatal(chunk.Error)
		}
		got += chunk.Content
		done = done || chunk.Done
	}
	if got != "final answer" || !done {
		t.Fatalf("unexpected tool stream got=%q done=%v", got, done)
	}
	requests := gateway.ToolRequests("default")
	foundPlan := false
	if len(requests) > 0 {
		for _, message := range requests[0].Messages {
			if strings.Contains(message.Content, "stream echo plan") {
				foundPlan = true
			}
		}
	}
	if !foundPlan {
		t.Fatalf("stream planning was not injected: %+v", requests)
	}
	loaded, err := repo.Load(context.Background(), "run-stream-tools")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCompleted {
		t.Fatalf("run not completed: %+v", loaded)
	}
}

func TestEngineRunDelegatesToSubAgent(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{
			ID:    "delegate-1",
			Name:  delegateToolName("researcher"),
			Input: json.RawMessage(`{"prompt":"research this"}`),
		}},
	})
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "research result"}})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "supervisor final"}},
	})
	events := &captureEvents{}
	scenario := baseScenario(false)
	supervisor := scenario.Agents["assistant"]
	supervisor.SubAgents = []string{"researcher"}
	supervisor.Policy.MaxSteps = 4
	scenario.Agents["assistant"] = supervisor
	scenario.Agents["researcher"] = core.Agent{Name: "researcher", LLM: "default", Instructions: "research", Description: "Research specialist"}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "delegate"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "supervisor final" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	requests := gateway.ToolRequests("default")
	if len(requests) != 2 || len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != delegateToolName("researcher") {
		t.Fatalf("sub-agent delegation tool not exposed: %+v", requests)
	}
	loaded, err := repo.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["agent.researcher.delegate-1"]; !ok {
		t.Fatalf("delegation output not persisted: %+v", loaded.StepOutputs)
	}
	if !events.has(core.EventToolCalled) || !events.has(core.EventToolReturned) {
		t.Fatalf("expected delegation lifecycle events, got %+v", events.types())
	}
}

func TestEngineRunReturnsAgentError(t *testing.T) {
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: runstateinmem.NewRepository(), LLM: llmmock.NewGateway()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "missing", Prompt: "hello"})
	if err == nil {
		t.Fatal("expected missing agent error")
	}
}

func TestEngineRunPausesForHumanGate(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gate := &capturingGate{repo: repo}
	engine, err := NewEngine(baseScenario(true), Dependencies{Runs: repo, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Prompt: "approve me"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token != "token" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if gate.state.NodeID != "before_final_answer" || gate.state.Version != 2 {
		t.Fatalf("unexpected checkpoint state: %+v", gate.state)
	}
}

func TestEngineRunRequiresGateWhenConfigured(t *testing.T) {
	engine, err := NewEngine(baseScenario(true), Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-1", Prompt: "hello"})
	if err == nil {
		t.Fatal("expected missing gate error")
	}
}

type capturingGate struct {
	repo  runstate.Repository
	state core.CheckpointState
}

type capturingGateway struct {
	req      llm.ChatRequest
	response string
}

type blockingGateway struct{}

func (blockingGateway) Supports(string, llm.Capability) bool {
	return true
}

func (blockingGateway) Chat(ctx context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	<-ctx.Done()
	return llm.ChatResponse{}, ctx.Err()
}

type retryGateway struct {
	failures int
	calls    int
	err      error
}

func (g *retryGateway) Supports(_ string, cap llm.Capability) bool {
	return cap == llm.CapChat
}

func (g *retryGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	g.calls++
	if g.calls <= g.failures {
		if g.err != nil {
			return llm.ChatResponse{}, g.err
		}
		return llm.ChatResponse{}, errors.New("temporary llm failure")
	}
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "retried"}}, nil
}

type permanentRetryError struct {
	message string
}

func (err permanentRetryError) Error() string {
	return err.message
}

func (err permanentRetryError) Retryable() bool {
	return false
}

func (g *capturingGateway) Supports(string, llm.Capability) bool {
	return true
}

func (g *capturingGateway) Chat(ctx context.Context, profile string, req llm.ChatRequest) (llm.ChatResponse, error) {
	g.req = req
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: g.response}}, nil
}

func (g *capturingGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	g.state = state
	snapshot, err := g.repo.Load(ctx, state.RunID)
	if err != nil {
		return "", err
	}
	snapshot.Status = runstate.RunStatusPaused
	snapshot.PendingGate = &state
	if err := g.repo.Save(ctx, &snapshot, state.Version); err != nil {
		return "", err
	}
	return "token", nil
}

func (g *capturingGate) Resume(context.Context, string, core.Decision, json.RawMessage) error {
	return nil
}

type mapToolRegistry map[string]core.ToolExecutor

func (r mapToolRegistry) ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	executor, ok := r[tool.Name]
	return executor, ok, nil
}

type echoTool struct{}

func (echoTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

type failingTool struct{}

func (failingTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	panic("tool should not execute")
}

type flakyTool struct {
	calls int
}

func (t *flakyTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	t.calls++
	if t.calls == 1 {
		return core.ToolResult{}, errors.New("temporary tool failure")
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

type largeOutputTool struct{}

func (largeOutputTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"data":"` + strings.Repeat("x", 1024) + `"}`)}, nil
}

type captureEvents struct {
	events []core.Event
}

type captureAudit struct {
	events []audit.Event
}

func (c *captureAudit) Record(_ context.Context, event audit.Event) error {
	c.events = append(c.events, event)
	return nil
}

func (c *captureAudit) has(typ audit.EventType) bool {
	for _, event := range c.events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func (c *captureEvents) Emit(_ context.Context, event core.Event) error {
	c.events = append(c.events, event)
	return nil
}

func (c *captureEvents) has(typ core.EventType) bool {
	for _, event := range c.events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func (c *captureEvents) count(typ core.EventType) int {
	count := 0
	for _, event := range c.events {
		if event.Type == typ {
			count++
		}
	}
	return count
}

func (c *captureEvents) types() []core.EventType {
	out := make([]core.EventType, 0, len(c.events))
	for _, event := range c.events {
		out = append(out, event.Type)
	}
	return out
}

func baseScenario(hitl bool) core.Scenario {
	checkpoints := []string(nil)
	if hitl {
		checkpoints = []string{"before_final_answer"}
	}
	return core.Scenario{
		Name: "scenario",
		Agents: map[string]core.Agent{
			"assistant": {
				Name:         "assistant",
				LLM:          "default",
				Instructions: "help",
			},
		},
		Orchestration: core.Orchestration{
			HumanInLoop: core.HumanInLoopPolicy{Enabled: hitl, Checkpoints: checkpoints},
		},
	}
}

func toolScenario(approval core.ApprovalPolicy, sideEffect core.SideEffectLevel, maxSteps int) core.Scenario {
	scenario := baseScenario(false)
	scenario.Runtime.MaxSteps = maxSteps
	scenario.Tools = map[string]core.Tool{
		"echo": {
			Name:        "echo",
			Type:        "builtin.echo",
			Description: "Echo the input",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Approval:    approval,
			SideEffect:  sideEffect,
		},
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo"}
	agent.Policy.MaxSteps = maxSteps
	scenario.Agents["assistant"] = agent
	return scenario
}
