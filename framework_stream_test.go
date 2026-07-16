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

func TestFrameworkStreamRunProductUIPresetFiltersInternalEvents(t *testing.T) {
	scenario := core.Scenario{
		Name: "stream-run-preset",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Memories: map[string]core.MemoryRef{
			"session": {Type: "in_memory", Scope: "session", Namespace: "stream-preset"},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Memory: "session"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "hi"}, {Done: true}}}
	var sinkSawInternal bool
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithEventSink(core.EventSinkFunc(func(_ context.Context, event core.Event) error {
			if event.Type == core.EventMemoryRead || event.Type == core.EventContextPrepared {
				sinkSawInternal = true
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	frames, err := fw.StreamRun(
		context.Background(),
		agentflow.RunRequest{RunID: "stream-preset-1", Agent: "assistant", Prompt: "hi"},
		agentflow.WithStreamEventFilterPreset(agentflow.EventFilterProductUI),
	)
	if err != nil {
		t.Fatal(err)
	}

	var streamSawInternal bool
	var eventCount int
	for frame := range frames {
		if frame.Kind != agentflow.StreamFrameEvent || frame.Event == nil {
			continue
		}
		eventCount++
		if frame.Event.Type == core.EventMemoryRead || frame.Event.Type == core.EventContextPrepared {
			streamSawInternal = true
		}
	}
	if eventCount == 0 {
		t.Fatal("expected some product events")
	}
	if !sinkSawInternal {
		t.Fatal("expected EventSink to still receive internal events")
	}
	if streamSawInternal {
		t.Fatal("product_ui StreamRun should hide MemoryRead/ContextPrepared")
	}
}
