package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineContinueAfterBeforeFinalAnswer(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final"}})
	scenario := core.Scenario{
		Name: "hitl",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationAutonomous,
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true, Checkpoints: []string{"before_final_answer"}},
		},
	}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
}

func TestEngineContinueAfterStructuredBeforeFinalAnswer(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"answer":"final"}`))
	scenario := core.Scenario{
		Name: "structured-hitl",
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
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationAutonomous,
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true, Checkpoints: []string{"before_final_answer"}},
		},
	}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.RunStructured(context.Background(), RunRequest{RunID: "run-structured-hitl", Agent: "assistant", Prompt: "hello"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-structured-hitl")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"final"}` {
		t.Fatalf("unexpected structured continue result: %+v", result)
	}
	snapshot, err := repo.Load(context.Background(), "run-structured-hitl")
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot.StepOutputs["final"].Inline) != `{"answer":"final"}` {
		t.Fatalf("structured final output not persisted raw: %+v", snapshot.StepOutputs["final"])
	}
}

func TestEnginePlanningExecuteTracksPlanSteps(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"steps":[{"goal":"echo","tool":"echo"}]}`))
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true, Execute: true, MaxSteps: 2}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-plan", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), "run-plan")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok {
		t.Fatal("expected plan step output")
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Steps) != 1 || state.Steps[0].Status != "done" {
		t.Fatalf("expected completed plan step, got %+v", state)
	}
}

func TestEngineContinueAfterMultiToolApprovalPause(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"first"}`)},
			{ID: "call-2", Name: "risky", Input: json.RawMessage(`{"query":"second"}`)},
			{ID: "call-3", Name: "echo", Input: json.RawMessage(`{"query":"third"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Tools["risky"] = core.Tool{
		Name:        "risky",
		Type:        "builtin.echo",
		Description: "Risky echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Approval:    core.ApprovalPause,
		SideEffect:  core.SideEffectWrite,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo", "risky"}
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:      repo,
		LLM:       gateway,
		HumanGate: gate,
		Tools:     mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-multi", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	snapshot, err := repo.Load(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	var pending []llm.ToolCall
	if raw := snapshot.Variables[checkpointToolCallsVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			t.Fatal(err)
		}
	}
	if len(pending) != 2 || pending[0].ID != "call-2" || pending[1].ID != "call-3" {
		t.Fatalf("expected remaining tool calls persisted, got %+v", pending)
	}
	if _, ok := snapshot.StepOutputs["tool.call-1"]; !ok {
		t.Fatal("expected first tool call to execute before pause")
	}
	token := paused.Token
	if token == "" {
		t.Fatal("expected pause token")
	}
	if err := gate.Resume(context.Background(), token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
	snapshot, err = repo.Load(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"call-1", "call-2", "call-3"} {
		if _, ok := snapshot.StepOutputs["tool."+id]; !ok {
			t.Fatalf("expected tool output for %s, got %+v", id, snapshot.StepOutputs)
		}
	}
}

func TestEngineContinueAfterToolApprovalUsesGovernanceAndAudit(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "risky", Input: json.RawMessage(`{"query":"approve"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Tools["risky"] = core.Tool{
		Name:        "risky",
		Type:        "builtin.echo",
		Description: "Risky echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Approval:    core.ApprovalPause,
		SideEffect:  core.SideEffectWrite,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"risky"}
	scenario.Agents["assistant"] = agent
	governanceCalls := 0
	audits := &captureAudit{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:      repo,
		LLM:       gateway,
		HumanGate: gate,
		Tools:     mapToolRegistry{"risky": echoTool{}},
		ToolPolicy: governance.ToolPolicyFunc(func(ctx context.Context, invocation governance.ToolInvocation) error {
			governanceCalls++
			if invocation.Tool != "risky" || invocation.SideEffect != core.SideEffectWrite {
				t.Fatalf("unexpected governance invocation: %+v", invocation)
			}
			return nil
		}),
		Audit: audits,
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-approved-governance", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-approved-governance")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
	if governanceCalls != 1 {
		t.Fatalf("expected approved tool to pass governance once, got %d", governanceCalls)
	}
	if !audits.has(audit.EventToolInvoked) {
		t.Fatalf("expected approved tool invocation audit, got %+v", audits.events)
	}
}

func TestEngineToolLoopMemoryUnchangedWhilePaused(t *testing.T) {
	repo := runstateinmem.NewRepository()
	memRepo := memoryinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"first"}`)},
			{ID: "call-2", Name: "risky", Input: json.RawMessage(`{"query":"second"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Tools["risky"] = core.Tool{
		Name:        "risky",
		Type:        "builtin.echo",
		Description: "Risky echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Approval:    core.ApprovalPause,
		SideEffect:  core.SideEffectWrite,
	}
	scenario.Memories = map[string]core.MemoryRef{
		"session": {Type: "in_memory", Scope: string(memory.ScopeSession), Namespace: "pause-session"},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	agent.Tools = []string{"echo", "risky"}
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:      repo,
		LLM:       gateway,
		HumanGate: gate,
		Memory:    map[string]memory.Repository{"session": memRepo},
		Tools:     mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ns := memory.Namespace{SessionID: "pause-session:assistant", Agent: "assistant", Scope: memory.ScopeSession}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-pause-mem", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	raw, err := memRepo.Get(context.Background(), ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Role != string(llm.RoleUser) {
		t.Fatalf("expected only user message in memory while paused, got %+v", stored)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-pause-mem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
	raw, err = memRepo.Get(context.Background(), ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 5 {
		t.Fatalf("expected user, assistant tool_calls, two tools, final assistant; got %+v", stored)
	}
	if stored[1].Role != string(llm.RoleAssistant) || len(stored[1].ToolCalls) != 2 {
		t.Fatalf("expected completed assistant turn in memory, got %+v", stored[1])
	}
	if stored[4].Role != string(llm.RoleAssistant) || stored[4].Content != "done" {
		t.Fatalf("expected final assistant answer, got %+v", stored[4])
	}
}

func TestEngineContinueAppliesHumanAmendment(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := &capturingGateway{response: "amended"}
	scenario := core.Scenario{
		Name: "hitl-amend",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationAutonomous,
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true, Checkpoints: []string{"before_final_answer"}},
		},
	}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-amend", Agent: "assistant", Prompt: "hello"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, json.RawMessage(`"please revise"`)); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-amend")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "amended" {
		t.Fatalf("expected amended output, got %q", result.Output)
	}
	found := false
	for _, msg := range gateway.req.Messages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "please revise") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected amendment in llm messages, got %+v", gateway.req.Messages)
	}
}

func TestEngineContinueToolApprovalRejectsCorruptToolCounts(t *testing.T) {
	repo := runstateinmem.NewRepository()
	scenario := core.Scenario{
		Name: "corrupt-counts",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalPause},
		},
	}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: mock.NewGateway()})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{
		RunID:        "run-corrupt",
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			checkpointKindVar:       json.RawMessage(`"tool_approval"`),
			checkpointAgentVar:      json.RawMessage(`"assistant"`),
			checkpointToolCountsVar: json.RawMessage(`not-json`),
		},
	}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	_, err = engine.ContinueAfterCheckpoint(context.Background(), "run-corrupt")
	if err == nil || !strings.Contains(err.Error(), "decode checkpoint tool counts") {
		t.Fatalf("expected decode error, got %v", err)
	}
}
