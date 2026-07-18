package core_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExtractStructuredOutputSuccess(t *testing.T) {
	text := "done\n```json\n{\"protocol\":\"agentbase.structured_output/v1\",\"run_state\":\"completed\",\"next_op\":\"none\"}\n```"
	ext := core.ExtractStructuredOutput(text)
	if ext.OutcomeKind != "structured_output" || ext.Block == nil {
		t.Fatalf("unexpected extract: %+v", ext)
	}
	if ext.Block["run_state"] != "completed" {
		t.Fatalf("run_state=%v", ext.Block["run_state"])
	}
}

func TestExtractStructuredOutputParseError(t *testing.T) {
	text := "```json\n{\"protocol\":\"agentbase.structured_output/v1\", run_state: broken}\n```"
	ext := core.ExtractStructuredOutput(text)
	if ext.OutcomeKind != "error_only" || ext.Error == "" {
		t.Fatalf("expected error_only, got %+v", ext)
	}
}

func TestBuildLifecyclePayloadRunCompletedStructured(t *testing.T) {
	corr := core.EpisodeCorrelation{EpisodeID: "ep-1", TriggerKind: "user", SessionID: "sess-1"}
	assistant := "ok\n```json\n{\"protocol\":\"agentbase.structured_output/v1\",\"run_state\":\"completed\",\"next_op\":\"none\"}\n```"
	output, err := json.Marshal(map[string]string{"text": assistant})
	if err != nil {
		t.Fatal(err)
	}
	raw := core.BuildLifecyclePayload(core.EventRunCompleted, output, corr)
	var payload core.RunTerminalPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OutcomeKind != "structured_output" || payload.Status != "completed" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.StructuredOutput) == 0 || payload.FinalText == "" {
		t.Fatalf("missing structured fields: %+v", payload)
	}
}

func TestBuildLifecyclePayloadRunStartedAlwaysObject(t *testing.T) {
	raw := core.BuildLifecyclePayload(core.EventRunStarted, nil, core.EpisodeCorrelation{})
	if string(raw) != "{}" {
		t.Fatalf("expected empty object, got %s", raw)
	}
}

func TestShouldEmitToDiagnosticUI(t *testing.T) {
	if !core.ShouldEmitToDiagnosticUI(core.EventMemoryRead) {
		t.Fatal("diagnostic preset must keep MemoryRead")
	}
	if core.ShouldEmitToProductUI(core.EventMemoryRead) {
		t.Fatal("product UI must filter MemoryRead")
	}
}
