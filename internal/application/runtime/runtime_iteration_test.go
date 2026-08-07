package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func queueToolTurn(gateway *mock.Gateway, id string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}},
	})
}

func queueFinalTurn(gateway *mock.Gateway, content string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: content}},
	})
}

func iterationEnvelopeAt(t *testing.T, ctx context.Context, blobs runstate.BlobStore, snapshot runstate.RunSnapshot, key string) iterationEnvelope {
	t.Helper()
	ref, ok := snapshot.StepOutputs[key]
	if !ok {
		t.Fatalf("expected step output %q, got keys %v", key, snapshot.StepOutputs)
	}
	raw, err := runstate.LoadStepOutput(ctx, blobs, ref)
	if err != nil {
		t.Fatalf("load %q: %v", key, err)
	}
	var envelope iterationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode %q: %v", key, err)
	}
	return envelope
}

func iterationPayloadSize(t *testing.T, ctx context.Context, blobs runstate.BlobStore, snapshot runstate.RunSnapshot, key string) int {
	t.Helper()
	ref, ok := snapshot.StepOutputs[key]
	if !ok {
		t.Fatalf("expected step output %q, got keys %v", key, snapshot.StepOutputs)
	}
	raw, err := runstate.LoadStepOutput(ctx, blobs, ref)
	if err != nil {
		t.Fatalf("load %q: %v", key, err)
	}
	return len(raw)
}

// TestEngineAutonomousIterationBoundariesPersisted pins the iteration
// checkpoint format: every completed LLM+tools iteration persists an
// incremental envelope under StepOutputs["auto:iter:<n>"] (delta against the
// previous boundary; the first boundary is a full snapshot); the final
// tool-free turn does not (the run completes instead). Folding the envelopes
// in order rebuilds the full conversation.
func TestEngineAutonomousIterationBoundariesPersisted(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurn(gateway, "call-1")
	queueToolTurn(gateway, "call-2")
	queueFinalTurn(gateway, "done")
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-iter", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed, got %+v", result)
	}
	snapshot, err := repo.Load(context.Background(), "run-iter")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["auto:iter:3"]; ok {
		t.Fatal("final tool-free turn must not persist an iteration boundary")
	}
	// First boundary is a full snapshot ending with the call-1 tool result.
	first := iterationEnvelopeAt(t, context.Background(), nil, snapshot, "auto:iter:1")
	if first.Format != iterationEnvelopeFormatFull {
		t.Fatalf("auto:iter:1 format=%q want %q", first.Format, iterationEnvelopeFormatFull)
	}
	last := first.Messages[len(first.Messages)-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "call-1" {
		t.Fatalf("auto:iter:1 must end with the tool result of call-1, got %+v", last)
	}
	// The second boundary is a delta carrying only iteration 2's messages
	// (assistant tool call + tool result) against the rebuilt prefix.
	second := iterationEnvelopeAt(t, context.Background(), nil, snapshot, "auto:iter:2")
	if second.Format != iterationEnvelopeFormatDelta {
		t.Fatalf("auto:iter:2 format=%q want %q", second.Format, iterationEnvelopeFormatDelta)
	}
	if second.Base != len(first.Messages) {
		t.Fatalf("auto:iter:2 base=%d want %d (conversation length after iteration 1)", second.Base, len(first.Messages))
	}
	if len(second.Messages) != 2 {
		t.Fatalf("auto:iter:2 delta must carry only iteration 2's messages, got %d", len(second.Messages))
	}
	last = second.Messages[len(second.Messages)-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "call-2" {
		t.Fatalf("auto:iter:2 must end with the tool result of call-2, got %+v", last)
	}
	// Folding both envelopes rebuilds the conversation with both tool turns.
	messages, _, err := engine.loadAutonomousConversation(context.Background(), "run-iter", snapshot.StepOutputs, 2)
	if err != nil {
		t.Fatal(err)
	}
	toolTurns := 0
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			toolTurns++
		}
	}
	if toolTurns != 2 {
		t.Fatalf("rebuilt conversation must carry both tool turns, got %d", toolTurns)
	}
}

// TestEngineAutonomousIterationDeltaWritesAreBounded: the per-iteration write
// size is O(new messages), not O(conversation): with n persisted iterations
// the nth payload stays the size of one iteration's delta, far below the
// full transcript a whole-conversation snapshot would write.
func TestEngineAutonomousIterationDeltaWritesAreBounded(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	const iterations = 5
	for i := 1; i <= iterations; i++ {
		queueToolTurn(gateway, "call-"+string(rune('0'+i)))
	}
	queueFinalTurn(gateway, "done")
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-bounded", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), "run-bounded")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sizes := make([]int, 0, iterations)
	for i := 1; i <= iterations; i++ {
		sizes = append(sizes, iterationPayloadSize(t, ctx, nil, snapshot, autonomousIterationKey(i)))
	}
	// Every boundary after the first is a two-message delta (assistant tool
	// call + tool result); its payload must not grow with the iteration count.
	for i := 2; i <= iterations; i++ {
		envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, autonomousIterationKey(i))
		if envelope.Format != iterationEnvelopeFormatDelta || len(envelope.Messages) != 2 {
			t.Fatalf("iteration %d must be a bounded delta, got format=%q messages=%d", i, envelope.Format, len(envelope.Messages))
		}
	}
	last, previous := sizes[iterations-1], sizes[iterations-2]
	if last > previous*2 {
		t.Fatalf("iteration payload size grows with n: sizes=%v", sizes)
	}
	// The last delta is much smaller than the full rebuilt transcript that
	// the old format would have persisted at the same boundary. (The delta
	// envelope also carries the fixed-size base_hash content anchor, so the
	// headroom factor is 2x; the transcript keeps growing with n while the
	// delta does not.)
	rebuilt, _, err := engine.loadAutonomousConversation(ctx, "run-bounded", snapshot.StepOutputs, iterations)
	if err != nil {
		t.Fatal(err)
	}
	fullRaw, err := json.Marshal(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if last*2 > len(fullRaw) {
		t.Fatalf("delta payload (%d bytes) is not clearly smaller than the full transcript (%d bytes)", last, len(fullRaw))
	}
}

// TestEngineAutonomousIterationCompactionFallsBackToFull: when the recorded
// baseline exceeds the current conversation length (context compaction shrank
// the transcript between boundaries), the boundary persists a full snapshot
// and resume replaces (not appends to) the rebuilt prefix.
func TestEngineAutonomousIterationCompactionFallsBackToFull(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-compact", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.coord.iterationBases.Store("run-compact", 100) // stale baseline from before a compaction
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "compacted answer"},
	}
	if err := engine.persistAutonomousIteration(ctx, "run-compact", 1, messages, newToolCallTracker(), 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(ctx, "run-compact")
	if err != nil {
		t.Fatal(err)
	}
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:1")
	if envelope.Format != iterationEnvelopeFormatFull || len(envelope.Messages) != len(messages) {
		t.Fatalf("expected a full fallback snapshot, got format=%q messages=%d", envelope.Format, len(envelope.Messages))
	}
	rebuilt, _, err := engine.loadAutonomousConversation(ctx, "run-compact", snapshot.StepOutputs, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt) != len(messages) {
		t.Fatalf("rebuilt %d messages want %d", len(rebuilt), len(messages))
	}
}

// TestEngineResumeAutonomousFromIteration: a run that crashed after two
// persisted iterations resumes from iteration 3 - the completed iterations
// are not re-issued to the LLM (asserted via the gateway's request log).
func TestEngineResumeAutonomousFromIteration(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurn(gateway, "call-1")
	queueToolTurn(gateway, "call-2")
	// Nothing queued for the third call: the mock reports ErrNoResponse,
	// which stands in for a worker crash mid-iteration-3.
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(ctx, RunRequest{RunID: "run-crash", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot, err := repo.Load(ctx, "run-crash")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
	if !HasAutonomousIterationProgress(snapshot) {
		t.Fatalf("expected persisted iteration progress, got keys %v", snapshot.StepOutputs)
	}

	// Simulate RetryFailedRun's Failed -> Running flip, then resume.
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.Save(runstate.ContextWithStatusTransitionOverride(ctx), &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	queueToolTurn(gateway, "call-3")
	queueFinalTurn(gateway, "recovered")
	before := len(gateway.ToolRequests("default"))
	result, err := engine.ResumeAutonomousFromIteration(ctx, "run-crash")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "recovered" {
		t.Fatalf("unexpected resume result: %+v", result)
	}
	requests := gateway.ToolRequests("default")
	resumed := requests[before:]
	if len(resumed) != 2 {
		t.Fatalf("resume must issue exactly the remaining iterations (3 + final), got %d requests", len(resumed))
	}
	// The first resumed request must already carry the persisted conversation:
	// two assistant tool-call turns and their tool results from iterations 1-2.
	toolTurns := 0
	for _, message := range resumed[0].Messages {
		if message.Role == llm.RoleTool {
			toolTurns++
		}
	}
	if toolTurns != 2 {
		t.Fatalf("resumed conversation must include both completed tool turns, got %d", toolTurns)
	}
}

// TestEngineResumeAutonomousFromIterationMatchesFullRun: the conversation
// rebuilt from delta envelopes after a crash is byte-identical to the
// conversation a never-crashed run sends at the same point, proving the
// incremental format loses nothing against the old full-snapshot behavior.
func TestEngineResumeAutonomousFromIterationMatchesFullRun(t *testing.T) {
	script := func(gateway *mock.Gateway, crashes bool) {
		gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
		queueToolTurn(gateway, "call-1")
		queueToolTurn(gateway, "call-2")
		if crashes {
			return // ErrNoResponse stands in for the crash inside iteration 3
		}
		queueToolTurn(gateway, "call-3")
		queueFinalTurn(gateway, "done")
	}
	newEngine := func(gateway *mock.Gateway, repo *runstateinmem.Repository) *Engine {
		engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 8), Dependencies{
			Runs:  repo,
			LLM:   gateway,
			Tools: mapToolRegistry{"echo": echoTool{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	ctx := context.Background()

	// Reference: the same run ID in a separate repository, no crash.
	refGateway := mock.NewGateway()
	script(refGateway, false)
	refEngine := newEngine(refGateway, runstateinmem.NewRepository())
	if _, err := refEngine.Run(ctx, RunRequest{RunID: "run-x", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	// The request at iteration 3 carries the full conversation after
	// iterations 1-2.
	reference := refGateway.ToolRequests("default")[2].Messages

	// Crashed: persists iterations 1-2 as envelopes, dies inside iteration 3.
	crashGateway := mock.NewGateway()
	script(crashGateway, true)
	crashRepo := runstateinmem.NewRepository()
	crashEngine := newEngine(crashGateway, crashRepo)
	if _, err := crashEngine.Run(ctx, RunRequest{RunID: "run-x", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot, err := crashRepo.Load(ctx, "run-x")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusRunning
	if err := crashRepo.Save(runstate.ContextWithStatusTransitionOverride(ctx), &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	queueToolTurn(crashGateway, "call-3")
	queueFinalTurn(crashGateway, "recovered")
	before := len(crashGateway.ToolRequests("default"))
	if _, err := crashEngine.ResumeAutonomousFromIteration(ctx, "run-x"); err != nil {
		t.Fatal(err)
	}
	rebuilt := crashGateway.ToolRequests("default")[before].Messages
	if len(rebuilt) != len(reference) {
		t.Fatalf("rebuilt conversation has %d messages, reference has %d", len(rebuilt), len(reference))
	}
	for i := range reference {
		if !messagesEqual(reference[i], rebuilt[i]) {
			t.Fatalf("message %d diverges:\nreference: %+v\nrebuilt:   %+v", i, reference[i], rebuilt[i])
		}
	}
}

func messagesEqual(a, b llm.Message) bool {
	ra, err := json.Marshal(a)
	if err != nil {
		return false
	}
	rb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ra) == string(rb)
}

// TestEngineResumeAutonomousFromIterationRejectsNonRunning pins the status
// contract: only a Running snapshot (RetryFailedRun flips before delegating)
// can re-enter through the iteration resume path.
func TestEngineResumeAutonomousFromIterationRejectsNonRunning(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{
		RunID:        "run-still-failed",
		ScenarioName: "scenario",
		Status:       runstate.RunStatusFailed,
		StepOutputs:  map[string]runstate.StepOutputRef{"auto:iter:1": {Inline: json.RawMessage(`[]`)}},
	}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResumeAutonomousFromIteration(context.Background(), "run-still-failed"); err == nil ||
		!strings.Contains(err.Error(), "requires running snapshot") {
		t.Fatalf("expected running-snapshot error, got %v", err)
	}
}

// TestEngineAutonomousIterationExternalizesToBlob: above the step-output
// threshold the iteration conversation is stored as a blob reference, and the
// resume path resolves it transparently.
func TestEngineAutonomousIterationExternalizesToBlob(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	blobs := blobinmem.NewStore()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurn(gateway, "call-1")
	queueToolTurn(gateway, "call-2")
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.StepOutputThreshold = 1 // externalize every step output
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		Blobs: blobs,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(ctx, RunRequest{RunID: "run-blob", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot, err := repo.Load(ctx, "run-blob")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"auto:iter:1", "auto:iter:2"} {
		ref, ok := snapshot.StepOutputs[key]
		if !ok {
			t.Fatalf("expected step output %q", key)
		}
		if ref.Blob == nil {
			t.Fatalf("%s must be externalized to the blob store above the threshold", key)
		}
		if len(ref.Blob.ID) == 0 || ref.Blob.Size == 0 {
			t.Fatalf("%s carries an invalid blob ref: %+v", key, ref.Blob)
		}
	}
	messages, _, err := engine.loadAutonomousConversation(ctx, "run-blob", snapshot.StepOutputs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("blob-backed iteration envelopes must resolve back to the conversation")
	}

	// Resume through the blob-backed checkpoint.
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.Save(runstate.ContextWithStatusTransitionOverride(ctx), &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	queueFinalTurn(gateway, "blob recovered")
	result, err := engine.ResumeAutonomousFromIteration(ctx, "run-blob")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "blob recovered" {
		t.Fatalf("unexpected blob resume result: %+v", result)
	}
}

// markRunningForIterationResume simulates RetryFailedRun's Failed -> Running
// flip so ResumeAutonomousFromIteration accepts the crashed run.
func markRunningForIterationResume(t *testing.T, ctx context.Context, repo *runstateinmem.Repository, runID string) runstate.RunSnapshot {
	t.Helper()
	snapshot, err := repo.Load(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning {
		snapshot.Status = runstate.RunStatusRunning
		if err := repo.Save(runstate.ContextWithStatusTransitionOverride(ctx), &snapshot, snapshot.Version); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

// TestEngineResumeAutonomousFromIterationRestoresDoomLoopCounts: the
// doom-loop tracker snapshot rides the iteration envelope, so a crash
// recovery on a fresh engine does not reset it. Two identical calls before
// the crash plus one after must trip the limit-3 doom-loop breaker on the
// third call; without the restored counts the post-crash call would execute.
func TestEngineResumeAutonomousFromIterationRestoresDoomLoopCounts(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	same := json.RawMessage(`{"query":"loop"}`)
	queueSame := func(id string) {
		gateway.QueueToolCall("default", llm.ToolCallResponse{
			ToolCalls: []llm.ToolCall{{ID: id, Name: "echo", Input: same}},
		})
	}
	queueSame("call-1")
	queueSame("call-2")
	// Nothing queued for the third call: ErrNoResponse stands in for a worker
	// crash mid-iteration-3.
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.DoomLoopLimit = 3
	var executed atomic.Int32
	engine1, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": countingTool{executed: &executed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine1.Run(ctx, RunRequest{RunID: "run-doom-crash", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	if got := executed.Load(); got != 2 {
		t.Fatalf("pre-crash executions = %d, want 2", got)
	}
	snapshot := markRunningForIterationResume(t, ctx, repo, "run-doom-crash")
	// The highest envelope must carry the tracker snapshot: 2 same-input
	// attempts recorded before the crash.
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:2")
	if envelope.State == nil {
		t.Fatal("iteration envelope must carry the run-state snapshot")
	}
	persisted, err := decodeToolCallTracker(envelope.State.ToolCounts)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.sameInputCount("echo", same); got != 2 {
		t.Fatalf("persisted same-input count = %d, want 2", got)
	}

	// Crash recovery on a fresh engine (empty process-local state).
	events := &captureEvents{}
	engine2, err := NewEngine(scenario, Dependencies{
		Runs:   repo,
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": countingTool{executed: &executed}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	tracker, stepsConsumed, _, err := engine2.restoreIterationRunState(ctx, "run-doom-crash", envelope.State, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := tracker.sameInputCount("echo", same); got != 2 {
		t.Fatalf("restored same-input count = %d, want 2", got)
	}
	if stepsConsumed != 2 {
		t.Fatalf("restored steps consumed = %d, want 2", stepsConsumed)
	}
	queueSame("call-3")
	queueFinalTurn(gateway, "stopped")
	result, err := engine2.ResumeAutonomousFromIteration(ctx, "run-doom-crash")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "stopped" {
		t.Fatalf("unexpected resume result: %+v", result)
	}
	// The third identical call must have been denied by the doom-loop breaker
	// before reaching the executor: the pre-crash counts were restored.
	if got := executed.Load(); got != 2 {
		t.Fatalf("post-resume executions = %d, want 2 (third call denied by doom loop)", got)
	}
	found := false
	for _, event := range events.events {
		if event.Type != core.EventToolDenied {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["kind"] == "doom_loop" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected doom_loop ToolDenied event after resume, got %+v", events.types())
	}
}

// TestEngineResumeAutonomousFromIterationRestoresUsageBudget: the accumulated
// usage tracker rides the iteration envelope, so max_total_tokens applies to
// the whole run across a crash recovery: 120+60 pre-crash plus 100 post-crash
// crosses the 200 budget only because the pre-crash totals were restored.
func TestEngineResumeAutonomousFromIterationRestoresUsageBudget(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueToolTurnWithUsage(gateway, "call-1", 120)
	queueToolTurnWithUsage(gateway, "call-2", 60)
	// Nothing queued for the third call: crash mid-iteration-3.
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Runtime.MaxTotalTokens = 200
	engine1, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine1.Run(ctx, RunRequest{RunID: "run-budget-crash", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot := markRunningForIterationResume(t, ctx, repo, "run-budget-crash")
	// The highest envelope must carry the accumulated usage: 180 total tokens.
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:2")
	if envelope.State == nil {
		t.Fatal("iteration envelope must carry the run-state snapshot")
	}
	usage, err := decodeUsageTracker(envelope.State.Usage)
	if err != nil {
		t.Fatal(err)
	}
	if got := usage.totalTokens(); got != 180 {
		t.Fatalf("persisted total tokens = %d, want 180", got)
	}

	engine2, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := engine2.restoreIterationRunState(ctx, "run-budget-crash", envelope.State, 2); err != nil {
		t.Fatal(err)
	}
	if got := engine2.usageTrackerFor("run-budget-crash").totalTokens(); got != 180 {
		t.Fatalf("restored total tokens = %d, want 180", got)
	}
	// The next call spends 100; only the restored 180 pushes the run over the
	// 200 budget (a reset tracker would see 100 and continue).
	queueToolTurnWithUsage(gateway, "call-3", 100)
	_, err = engine2.ResumeAutonomousFromIteration(ctx, "run-budget-crash")
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("expected ErrTokenBudgetExceeded after crash recovery, got %v", err)
	}
	if !strings.Contains(err.Error(), "consumed 280 total tokens") {
		t.Fatalf("budget error must report the cross-crash accumulated total, got %v", err)
	}
}

// TestEngineResumeAutonomousFromIterationRestoresApprovalMemory: the approval
// cache snapshot rides the iteration envelope, so a crash recovery on a fresh
// engine (new node, empty approval store) keeps the remembered allow and does
// not pause again for an already-approved call.
func TestEngineResumeAutonomousFromIterationRestoresApprovalMemory(t *testing.T) {
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
	allowInput := json.RawMessage(`{"q":"a"}`)
	// A prior approval cached the allow (as an approved pause resume would).
	store1 := toolorch.NewMemoryApprovalStore()
	toolorch.RememberAllow(store1, "run-approval-crash", "risky", allowInput)
	deps := func(store toolorch.ApprovalStore) Dependencies {
		return Dependencies{
			Runs:          repo,
			LLM:           gateway,
			HumanGate:     gate,
			Tools:         mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
			ApprovalStore: store,
		}
	}
	engine1, err := NewEngine(scenario, deps(store1))
	if err != nil {
		t.Fatal(err)
	}
	// Turn 1 hits the cached allow (no pause) and persists boundary 1 with the
	// approval cache in its state snapshot; turn 2 persists boundary 2. Nothing
	// is queued for turn 3: crash.
	queueRiskyTurn(gateway, "call-1", "a")
	queueToolTurn(gateway, "call-2")
	if _, err := engine1.Run(ctx, RunRequest{RunID: "run-approval-crash", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot := markRunningForIterationResume(t, ctx, repo, "run-approval-crash")
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:2")
	if envelope.State == nil || len(envelope.State.Approvals) == 0 {
		t.Fatal("iteration envelope must export the approval cache")
	}

	// Node switch: a fresh engine with an empty approval store resumes.
	engine2, err := NewEngine(scenario, deps(toolorch.NewMemoryApprovalStore()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := engine2.tooling.approvalStore.Get("run-approval-crash", toolorch.Key("risky", allowInput)); ok {
		t.Fatal("fresh node must start with an empty approval cache")
	}
	// The same call again: without the restored allow this pauses for approval
	// instead of completing.
	queueRiskyTurn(gateway, "call-3", "a")
	queueFinalTurn(gateway, "done")
	result, err := engine2.ResumeAutonomousFromIteration(ctx, "run-approval-crash")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "done" {
		t.Fatalf("expected completion without re-prompting after crash recovery, got %+v", result)
	}
}
