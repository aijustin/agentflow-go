package agentflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func collectStreamFrames(t *testing.T, ch <-chan agentflow.StreamFrame) []agentflow.StreamFrame {
	t.Helper()
	var frames []agentflow.StreamFrame
	timeout := time.After(15 * time.Second)
	for {
		select {
		case frame, ok := <-ch:
			if !ok {
				return frames
			}
			frames = append(frames, frame)
		case <-timeout:
			t.Fatal("timed out collecting stream frames")
		}
	}
}

func streamHubScenario() core.Scenario {
	return core.Scenario{
		Name: "stream-hub",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
}

func frameSummary(frames []agentflow.StreamFrame) []string {
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		switch frame.Kind {
		case agentflow.StreamFrameToken:
			out = append(out, "token:"+frame.Chunk.Content)
		case agentflow.StreamFrameEvent:
			out = append(out, "event:"+string(frame.Event.Type))
		case agentflow.StreamFrameDone:
			out = append(out, "done:"+string(frame.Result.Status))
		case agentflow.StreamFrameError:
			out = append(out, "error")
		case agentflow.StreamFrameEventsLost:
			out = append(out, "events_lost")
		}
	}
	return out
}

// TestStreamHubDualSubscriberReceivesSameFrames: an attached subscriber sees
// the exact frame sequence the primary StreamRun consumer receives.
func TestStreamHubDualSubscriberReceivesSameFrames(t *testing.T) {
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "hello"}, {Content: "world"}, {Done: true}}}
	fw, err := agentflow.New(streamHubScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-hub-dual", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := fw.AttachRunStream(ctx, "run-hub-dual")
	if err != nil {
		t.Fatal(err)
	}
	primaryFrames := collectStreamFrames(t, primary)
	attachedFrames := collectStreamFrames(t, attached)

	if len(primaryFrames) == 0 {
		t.Fatal("primary stream produced no frames")
	}
	got, want := frameSummary(attachedFrames), frameSummary(primaryFrames)
	if len(got) != len(want) {
		t.Fatalf("attached frames %v != primary frames %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attached frames %v != primary frames %v", got, want)
		}
	}
	last := attachedFrames[len(attachedFrames)-1]
	if last.Kind != agentflow.StreamFrameDone || last.Result.Status != runstate.RunStatusCompleted {
		t.Fatalf("attached stream must end with the completed done frame: %+v", last)
	}
}

// TestStreamHubAttachAfterCompletionReplays: attaching after the run
// completed (within the grace period) replays the whole ring and closes.
func TestStreamHubAttachAfterCompletionReplays(t *testing.T) {
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "hello"}, {Done: true}}}
	fw, err := agentflow.New(streamHubScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-hub-late", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	primaryFrames := collectStreamFrames(t, primary)

	attached, err := fw.AttachRunStream(ctx, "run-hub-late")
	if err != nil {
		t.Fatal(err)
	}
	attachedFrames := collectStreamFrames(t, attached)
	got, want := frameSummary(attachedFrames), frameSummary(primaryFrames)
	if len(got) != len(want) {
		t.Fatalf("replayed frames %v != primary frames %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replayed frames %v != primary frames %v", got, want)
		}
	}
}

// TestStreamHubPauseAttachResume: a paused run keeps its hub session; a
// subscriber attaching during the approval wait replays the paused stream,
// then observes the post-resume execution frames and the terminal done.
func TestStreamHubPauseAttachResume(t *testing.T) {
	scenario := core.Scenario{
		Name: "stream-hub-hitl",
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
	gateway := &captureGateway{
		script: func(call int) llm.ToolCallResponse {
			if call == 1 {
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "need approval"}},
					ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "gated", Input: json.RawMessage(`{"text":"go"}`)}},
				}
			}
			return llm.ToolCallResponse{
				ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "approved answer"}},
			}
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("gated", padEchoTool{name: "gated", pad: "ok"}),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-hub-hitl", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	primaryFrames := collectStreamFrames(t, primary)
	last := primaryFrames[len(primaryFrames)-1]
	if last.Kind != agentflow.StreamFrameDone || last.Result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused done frame, got %+v", last)
	}
	token := last.Result.Token

	// Attach during the approval wait: replay ends at the paused done frame
	// and the channel stays open.
	attached, err := fw.AttachRunStream(ctx, "run-hub-hitl")
	if err != nil {
		t.Fatal(err)
	}
	var sawPausedDone bool
	timeout := time.After(15 * time.Second)
	for !sawPausedDone {
		select {
		case frame, ok := <-attached:
			if !ok {
				t.Fatal("attached channel closed before resume; pause must not be a stream end")
			}
			if frame.Kind == agentflow.StreamFrameDone && frame.Result.Status == runstate.RunStatusPaused {
				sawPausedDone = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for the paused replay")
		}
	}

	continued, err := fw.ResumeAndContinue(ctx, token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if continued.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after resume, got %+v", continued)
	}

	// The post-resume execution keeps publishing into the same session.
	var sawCompletedDone bool
	for frame := range attached {
		if frame.Kind == agentflow.StreamFrameDone && frame.Result.Status == runstate.RunStatusCompleted {
			sawCompletedDone = true
		}
	}
	if !sawCompletedDone {
		t.Fatal("attached subscriber must observe the post-resume completed done frame")
	}
}

// TestStreamHubEventStoreFallback: a run with no live hub session (never
// streamed, or after process restart) is reassembled from the event store,
// ending with a synthetic done frame from the persisted run state.
func TestStreamHubEventStoreFallback(t *testing.T) {
	store := adapters.NewInMemoryEventStore()
	fw, err := agentflow.New(
		streamHubScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "stored answer"}),
		agentflow.WithEventSink(adapters.NewEventStoreSink(store)),
		agentflow.WithEventStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := fw.Run(ctx, agentflow.RunRequest{RunID: "run-hub-store", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected run result: %+v", result)
	}

	attached, err := fw.AttachRunStream(ctx, "run-hub-store")
	if err != nil {
		t.Fatal(err)
	}
	frames := collectStreamFrames(t, attached)
	var events int
	var done *agentflow.RunResult
	for _, frame := range frames {
		switch frame.Kind {
		case agentflow.StreamFrameEvent:
			events++
		case agentflow.StreamFrameDone:
			done = frame.Result
		}
	}
	if events == 0 {
		t.Fatal("expected replayed event frames from the event store")
	}
	if done == nil || done.Status != runstate.RunStatusCompleted || !strings.Contains(done.Output, "stored answer") {
		t.Fatalf("expected synthetic done from persisted state, got %+v", done)
	}
}

// TestStreamHubAttachUnknownRunWithoutStore errors when neither a live
// session nor an event store exists.
func TestStreamHubAttachUnknownRunWithoutStore(t *testing.T) {
	fw, err := agentflow.New(streamHubScenario(), agentflow.WithLLMGateway(fakeGateway{content: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.AttachRunStream(context.Background(), "run-unknown"); err == nil {
		t.Fatal("expected an error for an unknown run without an event store")
	}
}
