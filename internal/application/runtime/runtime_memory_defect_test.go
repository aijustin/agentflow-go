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
	"github.com/aijustin/agentflow-go/pkg/runstate"
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
	ns, ok, err := engine.memoryNamespace("run-cap", a)
	if err != nil || !ok {
		t.Fatalf("expected namespace, ok=%v err=%v", ok, err)
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
	ns, ok, err := engine.memoryNamespace("run-unb", a)
	if err != nil || !ok {
		t.Fatalf("expected namespace, ok=%v err=%v", ok, err)
	}
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

// M2: while running as a workflow node's agent, the engine stamps a
// conversation memory watermark (the run-scoped memory length before the agent
// appends any turns) into the run snapshot so workflow time-travel can rewind
// memory in step with the rewound step outputs.
func TestRunAgentRecordsConversationWatermarkForWorkflowNode(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	memRepo := memoryinmem.NewRepository()
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"conv": {Type: "in_memory", Scope: string(memory.ScopeConversation)},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "conv"
	scenario.Agents["assistant"] = agent

	engine, err := NewEngine(scenario, Dependencies{
		Runs:   repo,
		LLM:    &capturingGateway{response: "ok"},
		Memory: map[string]memory.Repository{"conv": memRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "run-wm", ScenarioName: scenario.Name, Status: runstate.RunStatusRunning}, 0); err != nil {
		t.Fatal(err)
	}
	a := scenario.Agents["assistant"]
	if err := engine.writeMemory(ctx, "run-wm", a, []memoryMessage{
		runTurnMemoryMessage(string(llm.RoleUser), "earlier-1"),
		runTurnMemoryMessage(string(llm.RoleAssistant), "earlier-2"),
	}); err != nil {
		t.Fatal(err)
	}
	nodeCtx := core.ContextWithWorkflowNode(ctx, "node-2")
	if _, err := engine.RunAgent(nodeCtx, "assistant", core.AgentInput{RunID: "run-wm", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(ctx, "run-wm")
	if err != nil {
		t.Fatal(err)
	}
	raw := snapshot.Variables["conversation_memory_watermarks"]
	if len(raw) == 0 {
		t.Fatal("expected conversation_memory_watermarks recorded for workflow node")
	}
	var marks map[string]conversationWatermark
	if err := json.Unmarshal(raw, &marks); err != nil {
		t.Fatal(err)
	}
	mark, ok := marks["node-2"]
	if !ok {
		t.Fatalf("expected watermark for node-2, got %+v", marks)
	}
	if mark.Agent != "assistant" || mark.Len != 2 {
		t.Fatalf("expected watermark {assistant,2}, got %+v", mark)
	}
}

// M2: RewindConversationMemory truncates a run-scoped conversation memory back
// to the requested number of messages, discarding later (rewound) turns, and is
// a no-op when keep is at or beyond the stored length.
func TestRewindConversationMemoryTruncatesStoredMessages(t *testing.T) {
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
	if err := engine.writeMemory(ctx, "run-rw", a, []memoryMessage{
		runTurnMemoryMessage(string(llm.RoleUser), "u1"),
		runTurnMemoryMessage(string(llm.RoleAssistant), "a1"),
		runTurnMemoryMessage(string(llm.RoleUser), "u2"),
		runTurnMemoryMessage(string(llm.RoleAssistant), "a2"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.RewindConversationMemory(ctx, "run-rw", "assistant", 2); err != nil {
		t.Fatal(err)
	}
	ns, ok, err := engine.memoryNamespace("run-rw", a)
	if err != nil || !ok {
		t.Fatalf("expected namespace, ok=%v err=%v", ok, err)
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
		t.Fatalf("expected memory truncated to 2, got %d", len(stored))
	}
	if stored[0].Content != "u1" || stored[1].Content != "a1" {
		t.Fatalf("expected earliest turns retained, got %q,%q", stored[0].Content, stored[1].Content)
	}
	if err := engine.RewindConversationMemory(ctx, "run-rw", "assistant", 10); err != nil {
		t.Fatal(err)
	}
	raw, err = memRepo.Get(ctx, ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected still 2 messages after no-op rewind, got %d", len(stored))
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
