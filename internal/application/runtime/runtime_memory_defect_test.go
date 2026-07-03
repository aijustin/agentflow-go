package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

// M1: with MemoryStoreLimit configured, writeMemory keeps only the most recent
// N messages; the default (0) keeps everything.
func TestWriteMemoryEnforcesStoreLimit(t *testing.T) {
	ctx := context.Background()
	memRepo := memoryinmem.NewRepository()
	scenario := baseScenario(false)
	profile := scenario.LLMs["default"]
	profile.Context = contextwindow.Policy{MemoryStoreLimit: 2}
	scenario.LLMs["default"] = profile
	scenario.Memories = map[string]core.MemoryRef{
		"conv": {Type: "in_memory", Scope: string(memory.ScopeConversation)},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "conv"
	scenario.Agents["assistant"] = agent

	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    &capturingGateway{response: "ok"},
		Memory: map[string]memory.Repository{"conv": memRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := scenario.Agents["assistant"]
	for i := 0; i < 5; i++ {
		if err := engine.writeMemory(ctx, "run-cap", a, []memoryMessage{
			runTurnMemoryMessage(string(llm.RoleUser), fmt.Sprintf("m%d", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ns, ok := engine.memoryNamespace("run-cap", a)
	if !ok {
		t.Fatal("expected namespace")
	}
	raw, err := memRepo.Get(ctx, ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected store capped at 2, got %d", len(stored))
	}
	if stored[0].Content != "m3" || stored[1].Content != "m4" {
		t.Fatalf("expected most recent messages retained, got %q,%q", stored[0].Content, stored[1].Content)
	}
}

func TestWriteMemoryDefaultIsUnbounded(t *testing.T) {
	ctx := context.Background()
	memRepo := memoryinmem.NewRepository()
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"conv": {Type: "in_memory", Scope: string(memory.ScopeConversation)},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "conv"
	scenario.Agents["assistant"] = agent

	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    &capturingGateway{response: "ok"},
		Memory: map[string]memory.Repository{"conv": memRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := scenario.Agents["assistant"]
	for i := 0; i < 4; i++ {
		if err := engine.writeMemory(ctx, "run-unb", a, []memoryMessage{
			runTurnMemoryMessage(string(llm.RoleUser), fmt.Sprintf("m%d", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ns, _ := engine.memoryNamespace("run-unb", a)
	raw, err := memRepo.Get(ctx, ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 {
		t.Fatalf("expected all 4 messages retained by default, got %d", len(stored))
	}
}

// M4: repairing tool_call/tool_result pairing after truncation surfaces an
// EventContextIncomplete describing the drops.
func TestEmitPairingIncompleteEmitsWarning(t *testing.T) {
	ctx := context.Background()
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    &capturingGateway{response: "ok"},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.emitPairingIncomplete(ctx, "run-x", pairingDrops{orphanToolResults: 1, unansweredToolCalls: 2})
	if !events.has(core.EventContextIncomplete) {
		t.Fatalf("expected ContextIncomplete event, got %+v", events.types())
	}
	before := events.count(core.EventContextIncomplete)
	engine.emitPairingIncomplete(ctx, "run-x", pairingDrops{})
	if events.count(core.EventContextIncomplete) != before {
		t.Fatal("expected no event when nothing was dropped")
	}
}

func TestEnforceToolCallPairingWithStatsCounts(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleTool, ToolCallID: "orphan", Content: `{"output":"x"}`},
		{Role: llm.RoleAssistant, Content: "thinking", ToolCalls: []llm.ToolCall{{ID: "unanswered", Name: "echo"}}},
	}
	out, drops := enforceToolCallPairingWithStats(messages)
	if drops.orphanToolResults != 1 {
		t.Fatalf("expected 1 orphan tool result, got %d", drops.orphanToolResults)
	}
	if drops.unansweredToolCalls != 1 {
		t.Fatalf("expected 1 unanswered tool call, got %d", drops.unansweredToolCalls)
	}
	if !drops.any() {
		t.Fatal("expected drops.any() to be true")
	}
	for _, msg := range out {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "orphan" {
			t.Fatal("orphan tool result should have been dropped")
		}
	}
}
