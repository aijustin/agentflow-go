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
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// F1 (DEFECT_REPORT second round): cancelling the StreamRun caller's context
// used to strand the hub session forever — the merger returned without a
// terminal frame, so attached subscribers hung and the session (ring
// included) was never reclaimed. The merger's exit path now settles the
// session with a terminal error frame.
func TestStreamRunCallerCancelSettlesHubSession(t *testing.T) {
	gateway := newTrickleStreamGateway()
	fw, err := agentflow.New(streamScenarioForGateway(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	frames, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-f1-cancel", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	if first := <-frames; first.Kind != agentflow.StreamFrameToken {
		t.Fatalf("expected a first token frame, got %+v", first)
	}
	attached, err := fw.AttachRunStream(context.Background(), "run-f1-cancel")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	// The attached subscriber must be released by a terminal frame, not hang
	// on the abandoned session (collectStreamFrames fails on a 15s hang).
	attachedFrames := collectStreamFrames(t, attached)
	last := attachedFrames[len(attachedFrames)-1]
	if last.Kind != agentflow.StreamFrameError && last.Kind != agentflow.StreamFrameDone {
		t.Fatalf("attached stream must end with a terminal frame after caller cancel, got %+v", last)
	}
	// A late attach within the grace window replays the settled stream and
	// closes instead of hanging on a zombie session.
	late, err := fw.AttachRunStream(context.Background(), "run-f1-cancel")
	if err != nil {
		t.Fatal(err)
	}
	lateFrames := collectStreamFrames(t, late)
	if len(lateFrames) == 0 {
		t.Fatal("late attach must replay the settled session")
	}
}

// F1: a detached StreamRun keeps executing after the caller cancels; the
// merger must keep the hub session authoritative (events rerouted to the hub,
// proper terminal Done at the end) instead of dropping every post-cancel
// frame with the tee and hanging attached subscribers.
func TestStreamRunDetachedCallerCancelKeepsHubStreamAlive(t *testing.T) {
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(streamScenarioForGateway(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	frames, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-f1-detached", Agent: "assistant", Prompt: "hi"}, agentflow.WithStreamDetached())
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	if first := <-frames; first.Kind != agentflow.StreamFrameToken || first.Chunk.Content != "partial " {
		t.Fatalf("unexpected first frame: %+v", first)
	}
	attached, err := fw.AttachRunStream(context.Background(), "run-f1-detached")
	if err != nil {
		t.Fatal(err)
	}
	// The caller goes away; the detached run keeps executing to completion.
	cancel()
	close(gateway.release)
	attachedFrames := collectStreamFrames(t, attached)
	last := attachedFrames[len(attachedFrames)-1]
	if last.Kind != agentflow.StreamFrameDone || last.Result.Status != runstate.RunStatusCompleted {
		t.Fatalf("detached hub stream must end with the completed done frame, got %+v", last)
	}
	awaitRunStatus(t, fw, "run-f1-detached", runstate.RunStatusCompleted)
}

// scriptToolGateway scripts tool-loop responses (including errors) and
// records requests, for HITL continue scenarios that need a transient
// provider failure.
type scriptToolGateway struct {
	mu       sync.Mutex
	requests []llm.ToolCallRequest
	script   func(call int) (llm.ToolCallResponse, error)
}

func (g *scriptToolGateway) Supports(string, llm.Capability) bool { return true }

func (g *scriptToolGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *scriptToolGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	call := len(g.requests)
	g.mu.Unlock()
	return g.script(call)
}

func hitlStreamScenario() core.Scenario {
	return core.Scenario{
		Name: "stream-hub-f2",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"gated"}},
		},
		Tools: map[string]core.Tool{
			"gated": {
				Name:        "gated",
				Type:        "builtin.gated",
				Approval:    core.ApprovalAlways,
				InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
}

// F2 (DEFECT_REPORT second round): a failed continue terminates the hub
// session with an error frame while the run stays Running with its checkpoint
// (a transient provider error). Retrying the continue must register a FRESH
// session — pre-fix, the run re-executed with zero hub frames and an attach
// replayed the stale terminal stream (ending in the old error frame).
func TestStreamHubContinueRetryReplacesTerminalSession(t *testing.T) {
	gateway := &scriptToolGateway{
		script: func(call int) (llm.ToolCallResponse, error) {
			switch call {
			case 1:
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "need approval"}},
					ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "gated", Input: json.RawMessage(`{"text":"go"}`)}},
				}, nil
			case 2:
				// Transient provider failure during the post-approval
				// continue: the run stays Running with its checkpoint, and
				// the hub session settles on an error frame.
				return llm.ToolCallResponse{}, errors.New("provider temporarily down")
			default:
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "approved answer"}},
				}, nil
			}
		},
	}
	fw, err := agentflow.New(
		hitlStreamScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("gated", padEchoTool{name: "gated", pad: "ok"}),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-f2-retry", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	primaryFrames := collectStreamFrames(t, primary)
	last := primaryFrames[len(primaryFrames)-1]
	if last.Kind != agentflow.StreamFrameDone || last.Result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused done frame, got %+v", last)
	}
	token := last.Result.Token

	if _, err := fw.ResumeAndContinue(ctx, token, core.DecisionApprove, nil); err == nil {
		t.Fatal("expected the transient provider failure to surface from ResumeAndContinue")
	}
	continued, err := fw.ContinueRun(ctx, "run-f2-retry")
	if err != nil {
		t.Fatal(err)
	}
	if continued.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after continue retry, got %+v", continued)
	}

	// Attaching after the retry must replay the retry's own session ending in
	// the completed done frame — not the stale terminal stream of the failed
	// continue.
	attached, err := fw.AttachRunStream(ctx, "run-f2-retry")
	if err != nil {
		t.Fatal(err)
	}
	attachedFrames := collectStreamFrames(t, attached)
	lastAttached := attachedFrames[len(attachedFrames)-1]
	if lastAttached.Kind != agentflow.StreamFrameDone || lastAttached.Result.Status != runstate.RunStatusCompleted {
		t.Fatalf("attach after continue retry must end with the completed done frame, got %+v", lastAttached)
	}
	if !strings.Contains(lastAttached.Result.Output, "approved answer") {
		t.Fatalf("done frame must carry the retried continue's output, got %q", lastAttached.Result.Output)
	}
}

// blockingChatGateway blocks its unary Chat until released, so a run can be
// observed mid-flight in the Running state.
type blockingChatGateway struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	content string
}

func (g *blockingChatGateway) Supports(string, llm.Capability) bool { return true }

func (g *blockingChatGateway) Chat(ctx context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	g.once.Do(func() { close(g.started) })
	select {
	case <-g.release:
		return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: g.content}}, nil
	case <-ctx.Done():
		return llm.ChatResponse{}, ctx.Err()
	}
}

// F2: the event-store replay fallback must not append a synthetic Done frame
// for a run that is still Running — Done tells consumers the stream reached
// its terminal state, which a live run has not.
func TestAttachRunStreamStoreReplayOmitsDoneForRunningRun(t *testing.T) {
	store := adapters.NewInMemoryEventStore()
	gateway := &blockingChatGateway{started: make(chan struct{}), release: make(chan struct{}), content: "late answer"}
	fw, err := agentflow.New(
		streamHubScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithEventSink(adapters.NewEventStoreSink(store)),
		agentflow.WithEventStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runDone := make(chan error, 1)
	go func() {
		_, err := fw.Run(ctx, agentflow.RunRequest{RunID: "run-f2-running", Agent: "assistant", Prompt: "hi"})
		runDone <- err
	}()
	<-gateway.started
	// The run is mid-flight (Running) with no live hub session: the attach
	// falls back to store replay.
	attached, err := fw.AttachRunStream(ctx, "run-f2-running")
	if err != nil {
		t.Fatal(err)
	}
	frames := collectStreamFrames(t, attached)
	for _, frame := range frames {
		if frame.Kind == agentflow.StreamFrameDone {
			t.Fatalf("store replay of a Running run must not synthesize a done frame, got %+v", frame.Result)
		}
	}
	close(gateway.release)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish after releasing the gateway")
	}
}
