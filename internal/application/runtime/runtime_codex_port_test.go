package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func TestTurnStopHookContinues(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "draft"}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final"}},
	})
	events := &captureEvents{}
	calls := 0
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
		TurnStopHook: func(ctx context.Context, info core.TurnStopInfo) (core.TurnStopDecision, error) {
			calls++
			if calls == 1 {
				return core.TurnStopDecision{Continue: true, ContinuationPrompt: "polish it"}, nil
			}
			return core.TurnStopDecision{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-stop-hook", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final" {
		t.Fatalf("output=%q", result.Output)
	}
	if events.count(core.EventTurnStopContinued) != 1 {
		t.Fatalf("expected TurnStopContinued, got %+v", events.types())
	}
}

func TestSamplingStepContextDeniesUnaadvertisedTool(t *testing.T) {
	step := toolorch.FreezeSamplingStepContext([]llm.ToolSpec{{Name: "echo"}})
	ctx := contextWithSamplingStep(context.Background(), step)
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.dispatchToolWithOptions(ctx, "run-step", core.Agent{Name: "assistant", Tools: []string{"echo"}}, llm.ToolCall{
		ID: "c1", Name: "http", Input: json.RawMessage(`{}`),
	}, newToolCallTracker(), toolDispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatal("expected step_context deny")
	}
}

func TestApprovalStoreSkipsSecondPause(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	orch := toolorch.NewStoreOrchestrator(store)
	input := json.RawMessage(`{"q":"x"}`)
	toolorch.RememberAllow(store, "run-cache", "echo", input)

	scenario := toolScenario(core.ApprovalPause, core.SideEffectWrite, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:             runstateinmem.NewRepository(),
		LLM:              llmmock.NewGateway(),
		Tools:            mapToolRegistry{"echo": echoTool{}},
		ToolOrchestrator: orch,
		ApprovalStore:    store,
		HumanGate:        &stubGate{token: "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.maybePauseToolCall(
		context.Background(),
		"run-cache",
		core.Agent{Name: "assistant"},
		[]llm.ToolCall{{ID: "c1", Name: "echo", Input: input}},
		nil,
		newToolCallTracker(),
		"prompt",
		1,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if paused != nil {
		t.Fatalf("cached allow must skip pause, got %+v", paused)
	}
}

func TestHITLDenyBreakerTripsOnSoftDeny(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hello"}`)}},
	})
	events := &captureEvents{}
	scenario := toolScenario(core.ApprovalAlways, core.SideEffectExternal, 4)
	scenario.Runtime.HITLDenyLimit = 1
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-deny-breaker", Agent: "assistant", Prompt: "use echo"})
	if err == nil || !strings.Contains(err.Error(), "HITL deny breaker") {
		t.Fatalf("expected deny breaker error, got %v", err)
	}
	if !events.has(core.EventHITLDenyBreakerTripped) {
		t.Fatalf("expected HITLDenyBreakerTripped, got %+v", events.types())
	}
}

func TestCachedDenySoftDeniesTool(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	input := json.RawMessage(`{"q":"blocked"}`)
	toolorch.RememberDeny(store, "run-cached-deny", "echo", input)
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:             runstateinmem.NewRepository(),
		LLM:              llmmock.NewGateway(),
		Tools:            mapToolRegistry{"echo": echoTool{}},
		Events:           events,
		ToolOrchestrator: toolorch.NewStoreOrchestrator(store),
		ApprovalStore:    store,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.dispatchToolWithOptions(context.Background(), "run-cached-deny", core.Agent{
		Name: "assistant", Tools: []string{"echo"},
	}, llm.ToolCall{ID: "c1", Name: "echo", Input: input}, newToolCallTracker(), toolDispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "cached") {
		t.Fatalf("expected cached deny, got %+v", result)
	}
	if !events.has(core.EventToolDenied) {
		t.Fatalf("expected ToolDenied, got %+v", events.types())
	}
}

func TestDrainInterjectionsDeferUntilPostCompact(t *testing.T) {
	repo := runstateinmem.NewRepository()
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-drain", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs: repo,
		LLM:  llmmock.NewGateway(),
		InterjectDrain: interjection.DrainPolicy{
			BeforeSample:          true,
			AfterToolBatch:        true,
			DeferUntilPostCompact: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Interject("run-drain", "steer now"); err != nil {
		t.Fatal(err)
	}
	agent := core.Agent{Name: "assistant"}
	msgs, err := engine.drainInterjectionsIfAllowed(context.Background(), "run-drain", agent, nil, interjection.DrainBeforeSample, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("before_sample must skip while compacted, got %d msgs", len(msgs))
	}
	if engine.interjections.PendingCount("run-drain") != 1 {
		t.Fatalf("expected pending interjection, got %d", engine.interjections.PendingCount("run-drain"))
	}
	msgs, err = engine.drainInterjectionsIfAllowed(context.Background(), "run-drain", agent, nil, interjection.DrainPostCompact, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "steer now") {
		t.Fatalf("post_compact should drain, got %+v", msgs)
	}
}

func TestCompactReminderInsertedBeforeLastUser(t *testing.T) {
	repo := runstateinmem.NewRepository()
	ctx := context.Background()
	planRaw, err := json.Marshal(planExecutionState{Steps: []planExecutionStep{{Goal: "finish task", Status: "pending"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:  "run-compact-inject",
		Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"plan": {Inline: planRaw},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	profile := scenario.LLMs["default"]
	profile.Context = contextwindow.Policy{
		Strategy:              contextwindow.StrategySlidingWindow,
		MaxInputTokens:        40,
		InjectCompactReminder: true,
	}
	scenario.LLMs["default"] = profile
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: llmmock.NewGateway()})
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system instructions that take some tokens"},
		{Role: llm.RoleUser, Content: "old user message with enough content to force truncation of history"},
		{Role: llm.RoleAssistant, Content: "old assistant reply that also consumes tokens in the window"},
		{Role: llm.RoleUser, Content: "latest question"},
	}
	prepared, stats := engine.prepareMessages(ctx, "run-compact-inject", scenario.Agents["assistant"], messages, profile)
	if !stats.NeedsReminder {
		t.Fatalf("expected NeedsReminder after compact, stats=%+v prepared=%d", stats, len(prepared))
	}
	reminderIdx, lastUserIdx := -1, -1
	for i, msg := range prepared {
		if msg.Metadata["context_window"] == "compact_reminder" {
			reminderIdx = i
		}
		if msg.Role == llm.RoleUser {
			lastUserIdx = i
		}
	}
	if reminderIdx < 0 {
		t.Fatalf("compact reminder missing: %+v", prepared)
	}
	if lastUserIdx < 0 || reminderIdx >= lastUserIdx {
		t.Fatalf("reminder must sit before last user: reminder=%d lastUser=%d msgs=%+v", reminderIdx, lastUserIdx, prepared)
	}
}

func TestRememberHITLRejectCachesDeny(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	repo := runstateinmem.NewRepository()
	input := json.RawMessage(`{"q":1}`)
	calls, err := json.Marshal([]llm.ToolCall{{ID: "c1", Name: "echo", Input: input}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:  "run-reject",
		Status: runstate.RunStatusPaused,
		Variables: map[string]json.RawMessage{
			checkpointToolCallsVar: calls,
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	scenario := toolScenario(core.ApprovalPause, core.SideEffectWrite, 4)
	scenario.Runtime.HITLDenyLimit = 2
	engine, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           llmmock.NewGateway(),
		ApprovalStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.RememberHITLReject(ctx, "run-reject")
	got, ok := store.Get("run-reject", toolorch.Key("echo", input))
	if !ok || got != toolorch.DecisionDeny {
		t.Fatalf("expected cached deny, got %v ok=%v", got, ok)
	}
}

type stubGate struct{ token string }

func (g *stubGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	return g.token, nil
}

func (g *stubGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return nil
}
