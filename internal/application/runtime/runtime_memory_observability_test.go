package runtime

import (
	"context"
	"encoding/json"
	"testing"

	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestMemoryReadPayloadIncludesProvenanceBreakdown(t *testing.T) {
	ctx := context.Background()
	memRepo := memoryinmem.NewRepository()
	ns := memory.Namespace{SessionID: "prov-session:assistant", Agent: "assistant", Scope: memory.ScopeSession}

	seed, err := json.Marshal(withMemoryProvenance(memoryMessage{
		Role:    string(llm.RoleUser),
		Content: "seeded chat",
	}, memory.ProvenanceIntegrator))
	if err != nil {
		t.Fatal(err)
	}
	if err := memRepo.Append(ctx, ns, "messages", seed); err != nil {
		t.Fatal(err)
	}

	events := &captureEvents{}
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"session": {Type: "in_memory", Scope: string(memory.ScopeSession), Namespace: "prov-session"},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent

	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    &capturingGateway{response: "answer"},
		Memory: map[string]memory.Repository{"session": memRepo},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Run(ctx, RunRequest{RunID: "run-prov", Agent: "assistant", Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}

	var readPayload map[string]any
	for _, event := range events.events {
		if event.Type != core.EventMemoryRead {
			continue
		}
		if err := json.Unmarshal(event.Payload, &readPayload); err != nil {
			t.Fatal(err)
		}
	}
	if readPayload == nil {
		t.Fatal("expected MemoryRead event")
	}
	if readPayload["stored_messages"].(float64) != 1 {
		t.Fatalf("expected stored_messages=1 before run writes, got %+v", readPayload)
	}
	byProv, ok := readPayload["messages_by_provenance"].(map[string]any)
	if !ok || byProv[memory.ProvenanceIntegrator].(float64) != 1 {
		t.Fatalf("expected integrator provenance breakdown, got %+v", readPayload)
	}
}

func TestEmitContextPreparedEmitsIncompleteWarning(t *testing.T) {
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

	disablePin := false
	engine.emitContextPrepared(ctx, "run-incomplete", contextwindowStats(t, disablePin))
	if !events.has(core.EventContextIncomplete) {
		t.Fatalf("expected ContextIncomplete event, got %+v", events.types())
	}
}

func contextwindowStats(t *testing.T, pinUser bool) contextwindow.Stats {
	t.Helper()
	result := contextwindow.New(contextwindow.Policy{
		Strategy:        contextwindow.StrategySlidingWindow,
		MaxInputTokens:  5,
		PinUserMessages: &pinUser,
	}).Prepare([]contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "first long user message with many tokens"},
		{Role: contextwindow.RoleAssistant, Content: "recent"},
	})
	if pinUser && result.Stats.DroppedUserMessages > 0 {
		t.Fatal("expected pin-user fixture to avoid dropped users")
	}
	if !pinUser && result.Stats.DroppedUserMessages == 0 {
		t.Fatal("expected disabled pin-user fixture to drop users")
	}
	return result.Stats
}
