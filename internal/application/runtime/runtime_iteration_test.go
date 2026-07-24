package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
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

func iterationMessages(t *testing.T, ctx context.Context, blobs runstate.BlobStore, snapshot runstate.RunSnapshot, key string) []llm.Message {
	t.Helper()
	ref, ok := snapshot.StepOutputs[key]
	if !ok {
		t.Fatalf("expected step output %q, got keys %v", key, snapshot.StepOutputs)
	}
	raw, err := runstate.LoadStepOutput(ctx, blobs, ref)
	if err != nil {
		t.Fatalf("load %q: %v", key, err)
	}
	var messages []llm.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("decode %q: %v", key, err)
	}
	return messages
}

// TestEngineAutonomousIterationBoundariesPersisted pins the iteration
// checkpoint format: every completed LLM+tools iteration persists the full
// conversation under StepOutputs["auto:iter:<n>"]; the final tool-free turn
// does not (the run completes instead).
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
	for key, wantToolCall := range map[string]string{"auto:iter:1": "call-1", "auto:iter:2": "call-2"} {
		messages := iterationMessages(t, context.Background(), nil, snapshot, key)
		last := messages[len(messages)-1]
		if last.Role != llm.RoleTool || last.ToolCallID != wantToolCall {
			t.Fatalf("%s must end with the tool result of %s, got %+v", key, wantToolCall, last)
		}
	}
	// The second boundary supersedes the first: it carries both tool turns.
	messages := iterationMessages(t, context.Background(), nil, snapshot, "auto:iter:2")
	toolTurns := 0
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			toolTurns++
		}
	}
	if toolTurns != 2 {
		t.Fatalf("auto:iter:2 must carry both tool turns, got %d", toolTurns)
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
	messages := iterationMessages(t, ctx, blobs, snapshot, "auto:iter:2")
	if len(messages) == 0 {
		t.Fatal("blob-backed iteration messages must resolve back to the conversation")
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
