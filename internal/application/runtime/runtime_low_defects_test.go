package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// DEFECT_REPORT D8 (regression lock-in): the run-completed lifecycle event
// must carry the final output content plus the storage reference — including
// when the output was externalized to the blob store. An earlier version
// emitted finalRef.Inline (nil for externalized outputs) as the whole payload.
func TestEngineRunCompletedEventCarriesContentAndBlobRef(t *testing.T) {
	blobs := blobinmem.NewStore()
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer well beyond the threshold"}})
	scenario := baseScenario(false)
	scenario.Runtime.StepOutputThreshold = 4
	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   repo,
		LLM:    gateway,
		Blobs:  blobs,
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-completed-event", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run, got %+v", result)
	}
	var completed *core.Event
	for i := range events.events {
		if events.events[i].Type == core.EventRunCompleted {
			completed = &events.events[i]
		}
	}
	if completed == nil {
		t.Fatalf("expected run completed event, got %+v", events.types())
	}
	var payload core.RunTerminalPayload
	if err := json.Unmarshal(completed.Payload, &payload); err != nil {
		t.Fatalf("completion event payload must be valid JSON: %v (payload=%s)", err, completed.Payload)
	}
	if payload.FinalText != "final answer well beyond the threshold" {
		t.Fatalf("completion event must carry the final output content, got final_text=%q", payload.FinalText)
	}
	var output struct {
		Text      string                 `json:"text"`
		OutputRef runstate.StepOutputRef `json:"output_ref"`
	}
	if err := json.Unmarshal(payload.Output, &output); err != nil {
		t.Fatalf("completion event output must decode: %v (output=%s)", err, payload.Output)
	}
	if output.Text != payload.FinalText {
		t.Fatalf("completion event output must keep the full content, got %q", output.Text)
	}
	if output.OutputRef.Blob == nil {
		t.Fatalf("completion event must carry the blob reference for an externalized output, got %s", payload.Output)
	}
	if output.OutputRef.Blob.ID == "" || output.OutputRef.Blob.Size <= 0 {
		t.Fatalf("blob reference must carry key/size, got %+v", output.OutputRef.Blob)
	}
	snapshot, err := repo.Load(context.Background(), "run-completed-event")
	if err != nil {
		t.Fatal(err)
	}
	stored := snapshot.StepOutputs["final"]
	if stored.Blob == nil || stored.Blob.ID != output.OutputRef.Blob.ID {
		t.Fatalf("event blob reference must match the persisted final step output ref: event=%+v stored=%+v", output.OutputRef.Blob, stored.Blob)
	}
}

// DEFECT_REPORT D9: a transient ErrStaleSnapshot (concurrent snapshot advance
// between load and CAS) must not strand the run in Running after the gate
// already issued a pause token — the paused-status save must retry.
func TestEngineEnsureRunPausedSurvivesStaleSnapshotConflict(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &conflictingRepository{Repository: inner, err: runstate.ErrStaleSnapshot}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inner.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-pause-stale", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	repo.failures.Store(1)
	if err := engine.ensureRunPaused(ctx, "run-pause-stale"); err != nil {
		t.Fatalf("paused status must survive one stale conflict, got %v", err)
	}
	loaded, err := inner.Load(ctx, "run-pause-stale")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusPaused {
		t.Fatalf("status=%s want Paused", loaded.Status)
	}
	if saves := repo.saves.Load(); saves < 2 {
		t.Fatalf("expected the paused-status write to be retried, saves=%d", saves)
	}
}

// DEFECT_REPORT D9 constraint (D7 interaction): when the gate already
// persisted the Paused transition itself (the built-in gates do),
// ensureRunPaused must not save again — rewriting would advance the snapshot
// version and supersede the pause token the gate just issued
// (ErrTokenSuperseded on resume).
func TestEngineEnsureRunPausedDoesNotRewriteAlreadyPausedRun(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &conflictingRepository{Repository: inner, err: runstate.ErrStaleSnapshot}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inner.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-already-paused", ScenarioName: "scenario", Status: runstate.RunStatusPaused,
	}, 0); err != nil {
		t.Fatal(err)
	}
	before, err := inner.Load(ctx, "run-already-paused")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.ensureRunPaused(ctx, "run-already-paused"); err != nil {
		t.Fatal(err)
	}
	after, err := inner.Load(ctx, "run-already-paused")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version {
		t.Fatalf("already-paused run must not be rewritten (version %d -> %d would supersede the pause token)", before.Version, after.Version)
	}
	if saves := repo.saves.Load(); saves != 0 {
		t.Fatalf("expected no save for an already-paused run, got %d", saves)
	}
}

// DEFECT_REPORT D12 (regression lock-in): when a tool attempt fails because
// the run context was cancelled, dispatchToolWithOptions returns early — but
// the orchestrator's AfterAttempt hook must still observe the attempt exactly
// once, or approval-cache attempt statistics lose it.
type recordingOrchestrator struct {
	mu            sync.Mutex
	afterAttempts []string
}

func (o *recordingOrchestrator) DecideApproval(context.Context, toolorch.ApprovalRequest) (toolorch.Decision, error) {
	return toolorch.DecisionAllow, nil
}

func (o *recordingOrchestrator) AfterAttempt(_ context.Context, _, tool string, _ json.RawMessage, _ toolorch.AttemptResult) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.afterAttempts = append(o.afterAttempts, tool)
	return nil
}

type cancelThenFailTool struct {
	cancel context.CancelFunc
}

func (t cancelThenFailTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	// The run is cancelled while the tool executes; the tool observes the
	// cancellation and returns it as its error.
	t.cancel()
	return core.ToolResult{}, context.Canceled
}

func TestEngineAfterAttemptRecordedOnCancelledToolExecution(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"x"}`)}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	orch := &recordingOrchestrator{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine, err := NewEngine(scenario, Dependencies{
		Runs:             runstateinmem.NewRepository(),
		LLM:              gateway,
		Tools:            mapToolRegistry{"echo": cancelThenFailTool{cancel: cancel}},
		ToolOrchestrator: orch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(ctx, RunRequest{RunID: "run-cancel-attempt", Agent: "assistant", Prompt: "use echo"}); err == nil {
		t.Fatal("expected the run to fail with the cancelled tool execution")
	}
	orch.mu.Lock()
	defer orch.mu.Unlock()
	if len(orch.afterAttempts) != 1 || orch.afterAttempts[0] != "echo" {
		t.Fatalf("AfterAttempt must be recorded exactly once for the cancelled attempt, got %+v", orch.afterAttempts)
	}
}

// DEFECT_REPORT D13: when the terminal snapshot save fails after the final
// output was externalized, the orphaned blob must be deleted best-effort
// (PurgeOrphanBlobs GC — covered by the root blob_gc_test.go — is the
// second-layer safety net).
func TestEnginePersistRunCompletedDeletesOrphanedBlobOnSaveFailure(t *testing.T) {
	blobs := blobinmem.NewStore()
	repo := runstateinmem.NewRepository()
	scenario := baseScenario(false)
	scenario.Runtime.StepOutputThreshold = 4
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, Blobs: blobs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A concurrently paused run makes the completion save fail with a
	// completion conflict after the final-output blob was already written.
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-orphan-blob", ScenarioName: "scenario", Status: runstate.RunStatusPaused,
	}, 0); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"text":"large final output forced into the blob store"}`)
	if _, err := engine.persistRunCompleted(ctx, "run-orphan-blob", raw); err == nil {
		t.Fatal("expected the completion save to fail on the paused run")
	} else {
		var conflict completionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("expected a completion conflict error, got %v", err)
		}
	}
	refs, err := blobs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("orphaned final-output blob must be deleted best-effort, %d remain", len(refs))
	}
}
