package agentflow_test

import (
	"context"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkStreamRunMergesTokensAndEvents(t *testing.T) {
	scenario := core.Scenario{
		Name: "stream-run",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "hello"}, {Done: true}}}
	var sawRunStarted bool
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithEventSink(core.EventSinkFunc(func(_ context.Context, event core.Event) error {
			if event.Type == core.EventRunStarted {
				sawRunStarted = true
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	frames, err := fw.StreamRun(context.Background(), agentflow.RunRequest{
		RunID:  "stream-run-1",
		Agent:  "assistant",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		tokens int
		events int
		done   *agentflow.RunResult
	)
	for frame := range frames {
		switch frame.Kind {
		case agentflow.StreamFrameToken:
			tokens++
		case agentflow.StreamFrameEvent:
			events++
		case agentflow.StreamFrameDone:
			done = frame.Result
		case agentflow.StreamFrameError:
			t.Fatalf("unexpected error frame: %v", frame.Err)
		}
	}
	if tokens == 0 {
		t.Fatal("expected token frames")
	}
	if events == 0 {
		t.Fatal("expected event frames from tee")
	}
	if !sawRunStarted {
		t.Fatal("expected underlying event sink to still receive RunStarted")
	}
	if done == nil || done.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected done result: %+v", done)
	}
	if done.RunID != "stream-run-1" {
		t.Fatalf("unexpected done payload: %+v", done)
	}
}
