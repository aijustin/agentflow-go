package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	meminmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type brokenRedactor struct{}

func (brokenRedactor) RedactOutput(context.Context, governance.OutputRedaction) (json.RawMessage, error) {
	return json.RawMessage(`not-json`), nil
}

func TestEngineRedactMemoryMessageRejectsInvalidRedactedJSON(t *testing.T) {
	repo := runstateinmem.NewRepository()
	memRepo := meminmem.NewRepository()
	scenario := baseScenario(false)
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	scenario.Memories = map[string]core.MemoryRef{
		"session": {Type: "custom", Scope: "session"},
	}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:           repo,
		Memory:         map[string]memory.Repository{"session": memRepo},
		OutputRedactor: brokenRedactor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.writeMemory(context.Background(), "run-redact", agent, []memoryMessage{
		{Role: "user", Content: "secret"},
	}); err == nil {
		t.Fatal("expected redact decode error")
	} else if !strings.Contains(err.Error(), "decode redacted memory message") {
		t.Fatalf("unexpected error: %v", err)
	}
}
