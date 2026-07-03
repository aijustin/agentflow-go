package runtime

import (
	"context"
	"encoding/json"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineIsBeforeFinalResumed(t *testing.T) {
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}
	if engine.isBeforeFinalResumed(runstate.RunSnapshot{}) {
		t.Fatal("expected false for missing variable")
	}
	if !engine.isBeforeFinalResumed(runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{beforeFinalResumedVar: json.RawMessage(`true`)},
	}) {
		t.Fatal("expected true for before_final_resumed flag")
	}
	if !engine.isBeforeFinalResumed(runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{checkpointResumedVar: json.RawMessage(`true`)},
	}) {
		t.Fatal("expected true for legacy checkpoint_resumed flag")
	}
	if engine.isBeforeFinalResumed(runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{beforeFinalResumedVar: json.RawMessage(`not-json`)},
	}) {
		t.Fatal("expected false for invalid json")
	}
}

func TestClearOrphanedCheckpointState(t *testing.T) {
	snapshot := runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{
			checkpointKindVar:     json.RawMessage(`"tool_approval"`),
			checkpointMessagesVar: json.RawMessage(`[]`),
		},
	}
	ClearOrphanedCheckpointState(&snapshot)
	if _, ok := snapshot.Variables[checkpointKindVar]; !ok {
		t.Fatal("expected intact checkpoint when messages are still present")
	}

	snapshot = runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{
			checkpointKindVar:      json.RawMessage(`"tool_approval"`),
			checkpointToolCallsVar: json.RawMessage(`[]`),
			checkpointPromptVar:    json.RawMessage(`"go"`),
		},
	}
	ClearOrphanedCheckpointState(&snapshot)
	for _, key := range checkpointVariableKeys() {
		if _, ok := snapshot.Variables[key]; ok {
			t.Fatalf("expected orphaned checkpoint key %q cleared", key)
		}
	}
}

func TestEngineCompleteStructuredRunReturnsPausedStatus(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-paused-structured", ScenarioName: "scenario", Status: runstate.RunStatusPaused,
	}, 0); err != nil {
		t.Fatal(err)
	}
	result, err := engine.completeStructuredRun(ctx, "run-paused-structured", json.RawMessage(`{"answer":"late"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestNonRunningCompletionResultRejectsCompletedStatus(t *testing.T) {
	if _, err := nonRunningCompletionResult("run-done", runstate.RunStatusCompleted); err == nil {
		t.Fatal("expected error for completed status conflict")
	}
}

func TestEngineCompleteStructuredRunSuccess(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-structured-ok", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"answer":"done"}`)
	result, err := engine.completeStructuredRun(ctx, "run-structured-ok", raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != string(raw) {
		t.Fatalf("unexpected result: %+v", result)
	}
	loaded, err := repo.Load(ctx, "run-structured-ok")
	if err != nil || loaded.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed snapshot, got %+v err=%v", loaded, err)
	}
}
