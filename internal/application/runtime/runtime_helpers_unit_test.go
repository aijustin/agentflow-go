package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineMarkRunFailedOrCancelledUsesCancelledStatus(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-cancel", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailedOrCancelled(ctx, "run-cancel", context.Canceled)
	loaded, err := repo.Load(ctx, "run-cancel")
	if err != nil || loaded.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled status, got %+v err=%v", loaded, err)
	}
}

func TestEngineStepOutputRefExternalizesLargePayload(t *testing.T) {
	blobs := blobinmem.NewStore()
	scenario := baseScenario(false)
	scenario.Runtime.StepOutputThreshold = 4
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  runstateinmem.NewRepository(),
		Blobs: blobs,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"payload":"large-value"}`)
	ref, err := engine.stepOutputRef(context.Background(), "run-blob", "step-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Blob == nil || len(ref.Inline) > 0 {
		t.Fatalf("expected blob ref, got %+v", ref)
	}
	stored, err := blobs.Get(context.Background(), *ref.Blob)
	if err != nil || string(stored) != string(raw) {
		t.Fatalf("blob content mismatch: %q err=%v", stored, err)
	}
}

func TestEngineStepOutputRefKeepsInlineBelowThreshold(t *testing.T) {
	scenario := baseScenario(false)
	scenario.Runtime.StepOutputThreshold = 1024
	engine, err := NewEngine(scenario, Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"small":true}`)
	ref, err := engine.stepOutputRef(context.Background(), "run-inline", "step-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Blob != nil || string(ref.Inline) != string(raw) {
		t.Fatalf("expected inline ref, got %+v", ref)
	}
}

func TestEngineEnsureRunPausedMarksRunningRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-running", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.ensureRunPaused(ctx, "run-running"); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(ctx, "run-running")
	if err != nil || loaded.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused status, got %+v err=%v", loaded, err)
	}
}

func TestEngineBeginRunRejectsDuplicateRunningRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := RunRequest{RunID: "run-dup", Agent: "assistant", Prompt: "hi"}
	if err := engine.beginRun(ctx, &req); err != nil {
		t.Fatal(err)
	}
	if err := engine.beginRun(ctx, &req); err == nil {
		t.Fatal("expected duplicate run error")
	}
}

func TestAutonomousRunInProgress(t *testing.T) {
	if !autonomousRunInProgress(runstate.RunSnapshot{}) {
		t.Fatal("empty step outputs means autonomous run in progress")
	}
	if autonomousRunInProgress(runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{"final": {Inline: json.RawMessage(`{}`)}},
	}) {
		t.Fatal("final step means workflow finished")
	}
	if !autonomousRunInProgress(runstate.RunSnapshot{
		StepOutputs: map[string]runstate.StepOutputRef{"tool.echo": {Inline: json.RawMessage(`{}`)}},
	}) {
		t.Fatal("tool step prefix means autonomous run in progress")
	}
}

func TestEngineMarkRunFailedPersistsReason(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-fail", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailed(ctx, "run-fail", errors.New("boom"))
	loaded, err := repo.Load(ctx, "run-fail")
	if err != nil || loaded.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed status, got %+v err=%v", loaded, err)
	}
}

func TestEngineMarkRunFailedOnCancelledRunEmitsCancelled(t *testing.T) {
	repo := runstateinmem.NewRepository()
	var emitted core.Event
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: repo,
		Events: core.EventSinkFunc(func(_ context.Context, event core.Event) error {
			emitted = event
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-cancelled", ScenarioName: "scenario", Status: runstate.RunStatusCancelled,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailed(ctx, "run-cancelled", errors.New("late failure"))
	if emitted.Type != core.EventRunCancelled {
		t.Fatalf("expected cancelled event, got %+v", emitted)
	}
}

func TestEngineResolveAgentName(t *testing.T) {
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}
	name, err := engine.ResolveAgentName("assistant")
	if err != nil || name != "assistant" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := engine.ResolveAgentName("missing"); err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func TestEngineLLMProfileRequiresNameWhenGatewayConfigured(t *testing.T) {
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: runstateinmem.NewRepository(),
		LLM:  mock.NewGateway(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.llmProfile(""); err == nil {
		t.Fatal("expected missing profile error")
	}
}

func TestEngineLLMProfileUnknownWithoutGateway(t *testing.T) {
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := engine.llmProfile("missing")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Provider != "" || profile.Model != "" {
		t.Fatalf("expected empty profile, got %+v", profile)
	}
}

func TestEngineEnsureRunActiveRejectsTerminalRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-done", ScenarioName: "scenario", Status: runstate.RunStatusCompleted,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.ensureRunActive(ctx, "run-done"); err == nil {
		t.Fatal("expected terminal run error")
	}
}
