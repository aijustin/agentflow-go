package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	meminmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type substringRedactor struct {
	find    string
	replace string
}

func (r substringRedactor) RedactOutput(_ context.Context, output governance.OutputRedaction) (json.RawMessage, error) {
	return json.RawMessage(strings.ReplaceAll(string(output.Data), r.find, r.replace)), nil
}

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

// TestEngineContextSummarizerRedactsBeforeSendingToLLM verifies that content
// dropped by context-window truncation is redacted before it is handed to
// the (potentially different, e.g. cheaper/third-party) profile used for
// "llm" summarization, so summarization cannot become a governance bypass.
func TestEngineContextSummarizerRedactsBeforeSendingToLLM(t *testing.T) {
	repo := runstateinmem.NewRepository()
	scenario := baseScenario(false)
	gateway := &capturingGateway{response: "a short summary"}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:           repo,
		LLM:            gateway,
		OutputRedactor: substringRedactor{find: "topsecret", replace: "[REDACTED]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.scenario.LLMs = map[string]core.LLMProfileRef{
		"default": {Provider: "mock", Model: "test"},
	}
	policy := contextwindow.Policy{
		Strategy:               contextwindow.StrategySlidingWindowWithSummary,
		SummaryMode:            "llm",
		MaxInputTokens:         5,
		SummaryTokens:          5,
		SystemPromptProtection: false,
	}
	raw := []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "the topsecret launch code is 12345 and must stay confidential"},
		{Role: contextwindow.RoleAssistant, Content: "acknowledged, topsecret code received and stored safely"},
		{Role: contextwindow.RoleUser, Content: "please repeat the topsecret code back to confirm receipt"},
	}
	engine.contextManager(context.Background(), "run-summary", policy).Prepare(raw)
	if strings.Contains(gateway.req.Messages[len(gateway.req.Messages)-1].Content, "topsecret") {
		t.Fatalf("expected summarizer input to be redacted, got: %s", gateway.req.Messages[len(gateway.req.Messages)-1].Content)
	}
	if !strings.Contains(gateway.req.Messages[len(gateway.req.Messages)-1].Content, "[REDACTED]") {
		t.Fatalf("expected redaction placeholder in summarizer input, got: %s", gateway.req.Messages[len(gateway.req.Messages)-1].Content)
	}
}
