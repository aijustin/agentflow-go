package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// newHITLFramework builds the standard before-final-answer HITL stack used by
// the recovery tests: one autonomous agent that always pauses before its
// final answer.
func newHITLFramework(t *testing.T, content string, opts ...agentflow.Option) *agentflow.Framework {
	t.Helper()
	base := []agentflow.Option{
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: content}),
	}
	fw, err := agentflow.New(builder.MinimalHumanInLoop("assistant"), append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return fw
}

func runUntilPaused(t *testing.T, fw *agentflow.Framework, runID string) agentflow.RunResult {
	t.Helper()
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused, got %+v", result)
	}
	return result
}

// TestFrameworkContinueRunRecoversApprovedRun covers the zombie family: the
// gate approved the run (Running again, checkpoint metadata attached) but
// nothing continued it. ContinueRun is the public recovery entry point and
// is idempotent once the run completes.
func TestFrameworkContinueRunRecoversApprovedRun(t *testing.T) {
	fw := newHITLFramework(t, "recovered answer")
	paused := runUntilPaused(t, fw, "run-zombie-recovery")

	// Approve without continuing: the deliberate zombie state.
	if err := fw.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-zombie-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning {
		t.Fatalf("expected Running after approve, got %s", snapshot.Status)
	}

	result, err := fw.ContinueRun(context.Background(), "run-zombie-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed result, got %+v", result)
	}
	if !strings.Contains(result.Output, "recovered answer") {
		t.Fatalf("unexpected output: %q", result.Output)
	}

	// Idempotent: a second ContinueRun on the Completed run returns the
	// persisted result instead of an error.
	again, err := fw.ContinueRun(context.Background(), "run-zombie-recovery")
	if err != nil {
		t.Fatalf("expected idempotent continue, got %v", err)
	}
	if again == nil || again.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed result on retry, got %+v", again)
	}
}

// TestFrameworkContinueRunClassifiesState pins the error contract for runs
// that are not "Running with pending checkpoint metadata".
func TestFrameworkContinueRunClassifiesState(t *testing.T) {
	fw := newHITLFramework(t, "x")
	paused := runUntilPaused(t, fw, "run-still-paused")
	_ = paused

	if _, err := fw.ContinueRun(context.Background(), "run-still-paused"); !errors.Is(err, runstate.ErrInvalidTransition) {
		t.Fatalf("expected classified error for paused run, got %v", err)
	}

	repo := fw.RunStateRepository()
	failed := &runstate.RunSnapshot{RunID: "run-failed", ScenarioName: "human-in-loop-demo", Status: runstate.RunStatusFailed}
	if err := repo.Save(context.Background(), failed, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fw.ContinueRun(context.Background(), "run-failed"); !errors.Is(err, runstate.ErrInvalidTransition) {
		t.Fatalf("expected classified error for failed run, got %v", err)
	}

	running := &runstate.RunSnapshot{RunID: "run-bare-running", ScenarioName: "human-in-loop-demo", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), running, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fw.ContinueRun(context.Background(), "run-bare-running"); err == nil ||
		!strings.Contains(err.Error(), "no pending checkpoint metadata") {
		t.Fatalf("expected no-checkpoint error, got %v", err)
	}
}

// TestFrameworkResumeAndContinueIdempotentOnCompleted: a duplicate resume of
// an already-Completed run returns the persisted result, not a token error.
func TestFrameworkResumeAndContinueIdempotentOnCompleted(t *testing.T) {
	fw := newHITLFramework(t, "final answer")
	paused := runUntilPaused(t, fw, "run-idempotent")

	result, err := fw.ResumeAndContinue(context.Background(), paused.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed, got %+v", result)
	}
	duplicate, err := fw.ResumeAndContinue(context.Background(), paused.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatalf("expected idempotent resume, got %v", err)
	}
	if duplicate.Status != runstate.RunStatusCompleted || !strings.Contains(duplicate.Output, "final answer") {
		t.Fatalf("unexpected duplicate result: %+v", duplicate)
	}
}

// blockingResumeGate pauses like the CLI gate but stalls the first Resume
// until released, so tests can deterministically race a second resume
// against an in-flight one. The pause token is the run ID itself, which the
// framework accepts for custom gates (see runIDFromToken).
type blockingResumeGate struct {
	repo    runstate.Repository
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *blockingResumeGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, g.repo, state.RunID)
	if err != nil {
		return "", err
	}
	snapshot.Status = runstate.RunStatusPaused
	snapshot.PendingGate = &state
	if err := g.repo.Save(ctx, &snapshot, snapshot.Version); err != nil {
		return "", err
	}
	return state.RunID, nil
}

func (g *blockingResumeGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	snapshot, err := runstate.LoadAuthorized(ctx, g.repo, token)
	if err != nil {
		return err
	}
	if snapshot.Status != runstate.RunStatusPaused {
		return runstate.ErrInvalidTransition
	}
	snapshot.Status = runstate.RunStatusRunning
	snapshot.PendingGate = nil
	return g.repo.Save(ctx, &snapshot, snapshot.Version)
}

// TestFrameworkConcurrentResumeReturnsResumeInProgress: without a run lease,
// the second of two concurrent resumes on the same run fails fast with
// ErrResumeInProgress instead of racing the pause token into an ambiguous
// ErrTokenSuperseded.
func TestFrameworkConcurrentResumeReturnsResumeInProgress(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gate := &blockingResumeGate{repo: repo, entered: make(chan struct{}), release: make(chan struct{})}
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithHumanGate(gate),
		agentflow.WithLLMGateway(fakeGateway{content: "done"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	paused := runUntilPaused(t, fw, "run-race")

	first := make(chan error, 1)
	go func() {
		_, err := fw.ResumeAndContinue(context.Background(), paused.Token, core.DecisionApprove, nil)
		first <- err
	}()
	<-gate.entered

	_, err = fw.ResumeAndContinue(context.Background(), paused.Token, core.DecisionApprove, nil)
	if !errors.Is(err, agentflow.ErrResumeInProgress) {
		t.Fatalf("expected ErrResumeInProgress for concurrent resume, got %v", err)
	}
	close(gate.release)
	if err := <-first; err != nil {
		t.Fatalf("first resume should succeed, got %v", err)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), repo, "run-race")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed after first resume, got %s", snapshot.Status)
	}
}

// TestFrameworkRetryFailedRunAutonomousWithoutProgress: an autonomous failed
// run with neither checkpoint metadata nor persisted iteration progress gets
// an explicit reason instead of silently re-running from scratch.
func TestFrameworkRetryFailedRunAutonomousWithoutProgress(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalAutonomous("assistant"),
		agentflow.WithLLMGateway(fakeGateway{content: "done"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	failed := &runstate.RunSnapshot{RunID: "run-no-progress", ScenarioName: "autonomous-assistant", Status: runstate.RunStatusFailed}
	if err := repo.Save(context.Background(), failed, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fw.RetryFailedRun(context.Background(), "run-no-progress"); err == nil ||
		!strings.Contains(err.Error(), "requires pending checkpoint metadata or persisted iteration progress") {
		t.Fatalf("expected explicit no-progress error, got %v", err)
	}
}

// autonomousIterationScenario builds one autonomous agent with a single
// no-approval echo tool, so the mock gateway can drive a multi-iteration
// tool loop.
func autonomousIterationScenario() core.Scenario {
	return core.Scenario{
		Name: "auto-iteration-resume",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
}

func queueIterationToolTurn(gateway *llmmock.Gateway, id string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}},
	})
}

func queueIterationFinalTurn(gateway *llmmock.Gateway, content string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: content}},
	})
}

// TestFrameworkRetryFailedRunAutonomousResumesFromIteration pins the new
// behavior: an autonomous run that crashed mid-loop (no HITL gate checkpoint)
// but with persisted iteration progress re-enters through RetryFailedRun and
// continues from the last persisted iteration's messages instead of failing
// with "requires pending checkpoint metadata". The gateway's request log
// proves the completed iterations are not re-issued to the LLM.
func TestFrameworkRetryFailedRunAutonomousResumesFromIteration(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueIterationToolTurn(gateway, "call-1")
	queueIterationToolTurn(gateway, "call-2")
	// Nothing queued for the third LLM call: ErrNoResponse stands in for a
	// worker crash inside iteration 3, after iterations 1-2 persisted.
	fw, err := agentflow.New(
		autonomousIterationScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-auto-crash", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-auto-crash")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
	for _, key := range []string{"auto:iter:1", "auto:iter:2"} {
		if _, ok := snapshot.StepOutputs[key]; !ok {
			t.Fatalf("expected persisted iteration %q, got keys %v", key, snapshot.StepOutputs)
		}
	}

	queueIterationToolTurn(gateway, "call-3")
	queueIterationFinalTurn(gateway, "recovered answer")
	before := len(gateway.ToolRequests("default"))
	result, err := fw.RetryFailedRun(context.Background(), "run-auto-crash")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || !strings.Contains(result.Output, "recovered answer") {
		t.Fatalf("unexpected retry result: %+v", result)
	}
	resumed := gateway.ToolRequests("default")[before:]
	if len(resumed) != 2 {
		t.Fatalf("resume must issue exactly the remaining iterations (3 + final), got %d requests", len(resumed))
	}
	// The first resumed request already carries the persisted conversation:
	// both completed tool turns from iterations 1-2.
	toolTurns := 0
	for _, message := range resumed[0].Messages {
		if message.Role == llm.RoleTool {
			toolTurns++
		}
	}
	if toolTurns != 2 {
		t.Fatalf("resumed conversation must include both completed tool turns, got %d", toolTurns)
	}

	// Idempotent: a second retry on the Completed run is rejected by the
	// status gate, not by re-execution.
	if _, err := fw.RetryFailedRun(context.Background(), "run-auto-crash"); err == nil {
		t.Fatal("expected retry of a completed run to be rejected")
	}
}

// TestFrameworkRetryFailedRunAutonomousResumeViaBlob: above the step-output
// threshold the iteration conversation lives in the blob store as a
// StepOutputRef; resume resolves the reference transparently.
func TestFrameworkRetryFailedRunAutonomousResumeViaBlob(t *testing.T) {
	scenario := autonomousIterationScenario()
	scenario.Runtime.StepOutputThreshold = 1 // externalize every step output
	blobs := blobinmem.NewStore()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueIterationToolTurn(gateway, "call-1")
	queueIterationToolTurn(gateway, "call-2")
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithBlobStore(blobs),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-auto-blob", Agent: "assistant", Prompt: "go"}); err == nil {
		t.Fatal("expected the run to fail when the LLM call has no queued response")
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-auto-blob")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"auto:iter:1", "auto:iter:2"} {
		ref, ok := snapshot.StepOutputs[key]
		if !ok {
			t.Fatalf("expected persisted iteration %q, got keys %v", key, snapshot.StepOutputs)
		}
		if ref.Blob == nil {
			t.Fatalf("%s must be externalized to the blob store above the threshold", key)
		}
	}
	queueIterationFinalTurn(gateway, "blob recovered")
	result, err := fw.RetryFailedRun(context.Background(), "run-auto-blob")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || !strings.Contains(result.Output, "blob recovered") {
		t.Fatalf("unexpected blob retry result: %+v", result)
	}
}

// TestFrameworkRetryFailedRunAutonomousFromCheckpoint: an autonomous failed
// run that still carries checkpoint metadata (e.g. after a lease-lost
// failure) re-enters through ContinueAfterCheckpoint and completes.
func TestFrameworkRetryFailedRunAutonomousFromCheckpoint(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "retried answer"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	failed := &runstate.RunSnapshot{
		RunID:        "run-retry-auto",
		ScenarioName: "human-in-loop-demo",
		Status:       runstate.RunStatusFailed,
		Variables: map[string]json.RawMessage{
			"checkpoint_kind":   json.RawMessage(`"before_final_answer"`),
			"checkpoint_prompt": json.RawMessage(`"retry me"`),
			"checkpoint_agent":  json.RawMessage(`"assistant"`),
			"resume_agent":      json.RawMessage(`"assistant"`),
		},
	}
	if err := repo.Save(context.Background(), failed, 0); err != nil {
		t.Fatal(err)
	}
	result, err := fw.RetryFailedRun(context.Background(), "run-retry-auto")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || !strings.Contains(result.Output, "retried answer") {
		t.Fatalf("unexpected retry result: %+v", result)
	}
}

// slowStreamGateway emits one content chunk, then blocks until released, so
// stream tests can cancel the caller mid-flight.
type slowStreamGateway struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSlowStreamGateway() *slowStreamGateway {
	return &slowStreamGateway{started: make(chan struct{}), release: make(chan struct{})}
}

func (g *slowStreamGateway) Supports(string, llm.Capability) bool { return true }

func (g *slowStreamGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func (g *slowStreamGateway) StreamChat(ctx context.Context, profile string, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 2)
	go func() {
		defer close(ch)
		g.once.Do(func() { close(g.started) })
		ch <- llm.ChatChunk{Content: "partial "}
		<-g.release
		ch <- llm.ChatChunk{Content: "final", Done: true}
	}()
	return ch, nil
}

func streamScenarioForGateway() core.Scenario {
	return core.Scenario{
		Name:   "stream-detached",
		LLMs:   map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "default"}},
	}
}

func awaitRunStatus(t *testing.T, fw *agentflow.Framework, runID string, want runstate.RunStatus) runstate.RunSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), runID)
		if err == nil && snapshot.Status == want {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, _ := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), runID)
	t.Fatalf("run did not reach %s in time (last: %+v)", want, snapshot)
	return runstate.RunSnapshot{}
}

// TestFrameworkStreamDetachedCompletesAfterCallerCancel: in detached mode a
// caller disconnect does not cancel the run; it keeps executing in the
// background and persists its terminal result.
func TestFrameworkStreamDetachedCompletesAfterCallerCancel(t *testing.T) {
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(streamScenarioForGateway(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := fw.Stream(agentflow.StreamDetached(ctx), agentflow.RunRequest{RunID: "run-detached", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	first := <-chunks
	if first.Content != "partial " {
		t.Fatalf("unexpected first chunk: %+v", first)
	}
	// Client disconnects mid-stream.
	cancel()
	close(gateway.release)
	// Drain whatever the detached run still forwards; the channel closes once
	// the engine notices the caller is gone.
	for range chunks {
	}
	snapshot := awaitRunStatus(t, fw, "run-detached", runstate.RunStatusCompleted)
	if msg := snapshot.Variables[runstate.VarRunErrorMessage]; len(msg) != 0 {
		t.Fatalf("detached run must not persist an error, got %s", msg)
	}
}

// TestFrameworkStreamCallerCancelMarksCancelled contrasts the default
// (non-detached) behavior: a caller disconnect marks the run Cancelled.
func TestFrameworkStreamCallerCancelMarksCancelled(t *testing.T) {
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(streamScenarioForGateway(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := fw.Stream(ctx, agentflow.RunRequest{RunID: "run-attached", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	<-chunks
	cancel()
	close(gateway.release)
	for range chunks {
	}
	awaitRunStatus(t, fw, "run-attached", runstate.RunStatusCancelled)
}

// TestFrameworkResumeRunByIDRejectWithContinue: a reject that asks to
// continue must still end in the reject terminal state — it must not enter
// continueRun against the just-cancelled snapshot.
func TestFrameworkResumeRunByIDRejectWithContinue(t *testing.T) {
	fw := newHITLFramework(t, "should never run")
	runUntilPaused(t, fw, "run-reject-continue")

	result, err := fw.ResumeRunByID(context.Background(), "run-reject-continue", core.DecisionReject, nil, true)
	if err != nil {
		t.Fatalf("reject with continue must not error, got %v", err)
	}
	if result.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled result, got %+v", result)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-reject-continue")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled snapshot, got %s", snapshot.Status)
	}
}

// TestFrameworkResumeRunByIDRejectWithoutContinue: the reject-only path
// keeps working unchanged.
func TestFrameworkResumeRunByIDRejectWithoutContinue(t *testing.T) {
	fw := newHITLFramework(t, "should never run")
	runUntilPaused(t, fw, "run-reject-plain")

	result, err := fw.ResumeRunByID(context.Background(), "run-reject-plain", core.DecisionReject, nil, false)
	if err != nil {
		t.Fatalf("reject must not error, got %v", err)
	}
	if result.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled result, got %+v", result)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-reject-plain")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled snapshot, got %s", snapshot.Status)
	}
}

// TestFrameworkResumeRunByIDAuthorizationHook: the hook gates token minting;
// without it the historical run-ID-only behavior is unchanged.
func TestFrameworkResumeRunByIDAuthorizationHook(t *testing.T) {
	hookErr := errors.New("not your run")
	var hookedRun string
	fw := newHITLFramework(t, "done", agentflow.WithResumeAuthorizationHook(func(ctx context.Context, runID string) error {
		hookedRun = runID
		return hookErr
	}))
	runUntilPaused(t, fw, "run-hook")
	if _, err := fw.ResumeRunByID(context.Background(), "run-hook", core.DecisionApprove, nil, true); !errors.Is(err, hookErr) {
		t.Fatalf("expected hook error to abort resume, got %v", err)
	}
	if hookedRun != "run-hook" {
		t.Fatalf("hook saw run %q, want run-hook", hookedRun)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-hook")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("hook-denied resume must leave the run paused, got %s", snapshot.Status)
	}

	// Without a hook the same call succeeds (backward compatibility).
	fwOpen := newHITLFramework(t, "done")
	runUntilPaused(t, fwOpen, "run-open")
	result, err := fwOpen.ResumeRunByID(context.Background(), "run-open", core.DecisionApprove, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed without hook, got %+v", result)
	}
}

// TestFrameworkHITLTokenRotation: tokens minted under the secondary key keep
// verifying while the framework signs with the primary.
func TestFrameworkHITLTokenRotation(t *testing.T) {
	fw := newHITLFramework(t, "rotated answer",
		agentflow.WithHITLTokenRotation([]byte("primary-secret-0123"), []byte("test-secret-012345"), nil))
	paused := runUntilPaused(t, fw, "run-rotated")
	// The pause token was minted by the rotation signer; resume must verify.
	result, err := fw.ResumeAndContinue(context.Background(), paused.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed under rotation, got %+v", result)
	}
}

// TestFrameworkHITLWeakSecretRejected: weak HITL secrets fail at wiring time.
func TestFrameworkHITLWeakSecretRejected(t *testing.T) {
	_, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("short"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
	)
	if err == nil {
		t.Fatal("expected weak secret rejection at framework construction")
	}
}

// TestFrameworkResumeApproveWithoutGatewayFailsAndRecovers: the reported
// regression — resume approve with no LLM gateway must surface the error AND
// mark the run Failed (not strand it in Running), keeping the checkpoint so
// RetryFailedRun completes once a gateway is wired.
func TestFrameworkResumeApproveWithoutGatewayFailsAndRecovers(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	paused := &runstate.RunSnapshot{
		RunID:        "run-resume-no-gw",
		ScenarioName: "human-in-loop-demo",
		Status:       runstate.RunStatusPaused,
		Variables: map[string]json.RawMessage{
			"checkpoint_kind":   json.RawMessage(`"before_final_answer"`),
			"checkpoint_prompt": json.RawMessage(`"finish me"`),
			"checkpoint_agent":  json.RawMessage(`"assistant"`),
			"resume_agent":      json.RawMessage(`"assistant"`),
		},
	}
	if err := repo.Save(context.Background(), paused, 0); err != nil {
		t.Fatal(err)
	}
	_, err = fw.ResumeRunByID(context.Background(), "run-resume-no-gw", core.DecisionApprove, nil, true)
	if err == nil || !strings.Contains(err.Error(), "llm gateway is required") {
		t.Fatalf("expected gateway-required error, got %v", err)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), repo, "run-resume-no-gw")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected Failed (not stranded Running), got %s", snapshot.Status)
	}
	if got := string(snapshot.Variables["run_error_message"]); !strings.Contains(got, "llm gateway is required") {
		t.Fatalf("expected gateway reason on snapshot, got %s", got)
	}
	if got := string(snapshot.Variables["checkpoint_kind"]); got != `"before_final_answer"` {
		t.Fatalf("checkpoint must be kept for RetryFailedRun, got %s", got)
	}

	// Wire a gateway (second framework over the same repository): the failed
	// run recovers from its intact checkpoint.
	fw2, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "recovered answer"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw2.RetryFailedRun(context.Background(), "run-resume-no-gw")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || !strings.Contains(result.Output, "recovered answer") {
		t.Fatalf("expected recovery via RetryFailedRun, got %+v", result)
	}
}
