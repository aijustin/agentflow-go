package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func queueToolTurnWithUsage(gateway *mock.Gateway, id string, total int) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Usage: llm.TokenUsage{InputTokens: total - 10, OutputTokens: 10, TotalTokens: total}},
		ToolCalls:    []llm.ToolCall{{ID: id, Name: "echo", Input: json.RawMessage(`{}`)}},
	})
}

func queueFinalTurnWithUsage(gateway *mock.Gateway, content string, total int) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message: llm.Message{Role: llm.RoleAssistant, Content: content},
			Usage:   llm.TokenUsage{InputTokens: total - 10, OutputTokens: 10, TotalTokens: total},
		},
	})
}

// TestEngineTokenBudgetExceeded: once the accumulated provider-reported total
// crosses max_total_tokens the run fails with the sentinel and the terminal
// event attributes termination_reason=budget_exceeded.
func TestEngineTokenBudgetExceeded(t *testing.T) {
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurnWithUsage(gateway, "call-1", 120)
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.MaxTotalTokens = 100
	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-budget", Agent: "assistant", Prompt: "go"})
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("expected ErrTokenBudgetExceeded, got %v", err)
	}
	payload := events.terminalPayload(t, core.EventRunFailed)
	if payload.TerminationReason != core.TerminationReasonBudgetExceeded {
		t.Fatalf("TerminationReason=%q want %q", payload.TerminationReason, core.TerminationReasonBudgetExceeded)
	}
}

// TestEngineTokenBudgetWithinLimit: usage under the budget never trips.
func TestEngineTokenBudgetWithinLimit(t *testing.T) {
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurnWithUsage(gateway, "call-1", 60)
	queueFinalTurnWithUsage(gateway, "done", 60)
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.MaxTotalTokens = 1000
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-budget-ok", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "done" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestEngineTokenBudgetAccumulatesAcrossPauseResume: the pre-pause usage is
// restored from the checkpoint on the resuming node, so the budget check sees
// the whole run's spend (120 before the pause + 100 after > 200) instead of
// only the post-resume call.
func TestEngineTokenBudgetAccumulatesAcrossPauseResume(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	scenario := approvalMigrationScenario()
	scenario.Runtime.MaxTotalTokens = 200
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Usage: llm.TokenUsage{InputTokens: 110, OutputTokens: 10, TotalTokens: 120}},
		ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "risky", Input: json.RawMessage(`{"q":"a"}`)}},
	})
	// Post-resume call spends 100; only the checkpointed 120 pushes the run
	// over the 200 budget.
	queueFinalTurnWithUsage(gateway, "done", 100)
	deps := func() Dependencies {
		return Dependencies{
			Runs:      repo,
			LLM:       gateway,
			HumanGate: gate,
			Tools:     mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
		}
	}
	engine1, err := NewEngine(scenario, deps())
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine1.Run(ctx, RunRequest{RunID: "run-budget-pause", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	// Node switch: a fresh engine (empty usage tracker) resumes.
	engine2, err := NewEngine(scenario, deps())
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Resume(ctx, paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	_, err = engine2.ContinueAfterCheckpoint(ctx, "run-budget-pause")
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("expected ErrTokenBudgetExceeded after resume, got %v", err)
	}
	snapshot, loadErr := repo.Load(ctx, "run-budget-pause")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
}
