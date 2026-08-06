package core_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestBuildLifecyclePayloadRunCompleted(t *testing.T) {
	corr := core.EpisodeCorrelation{EpisodeID: "ep-1", TriggerKind: "manual", SessionID: "sess-1"}
	raw := core.BuildLifecyclePayload(core.EventRunCompleted, json.RawMessage(`{"answer":"ok"}`), corr)
	var payload core.RunTerminalPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "completed" || payload.EpisodeID != "ep-1" || payload.TriggerKind != "manual" || payload.SessionID != "sess-1" {
		t.Fatalf("unexpected terminal payload: %+v", payload)
	}
	if string(payload.Output) != `{"answer":"ok"}` {
		t.Fatalf("unexpected output: %s", payload.Output)
	}
}

func TestBuildLifecyclePayloadRunStartedMergesCorrelation(t *testing.T) {
	corr := core.EpisodeCorrelation{EpisodeID: "ep-2", TriggerKind: "webhook"}
	raw := core.BuildLifecyclePayload(core.EventRunStarted, nil, corr)
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["episode_id"] != "ep-2" || fields["trigger_kind"] != "webhook" {
		t.Fatalf("unexpected started payload: %#v", fields)
	}
}

func TestIsLifecycleEvent(t *testing.T) {
	if !core.IsLifecycleEvent(core.EventRunPaused) {
		t.Fatal("RunPaused should be lifecycle")
	}
	if core.IsLifecycleEvent(core.EventToolCalled) {
		t.Fatal("ToolCalled should not be lifecycle")
	}
}

func TestBuildLifecyclePayloadTerminationReason(t *testing.T) {
	corr := core.EpisodeCorrelation{EpisodeID: "ep-1"}
	cases := []struct {
		name    string
		typ     core.EventType
		payload json.RawMessage
		want    string
	}{
		{name: "completed defaults to completed", typ: core.EventRunCompleted, payload: json.RawMessage(`{"text":"ok"}`), want: core.TerminationReasonCompleted},
		{name: "failed without reason defaults to error", typ: core.EventRunFailed, payload: json.RawMessage(`{"error":"boom"}`), want: core.TerminationReasonError},
		{name: "failed carries emitter reason", typ: core.EventRunFailed, payload: json.RawMessage(`{"error":"boom","termination_reason":"max_steps_exceeded"}`), want: core.TerminationReasonMaxStepsExceeded},
		{name: "failed llm error reason", typ: core.EventRunFailed, payload: json.RawMessage(`{"error":"boom","termination_reason":"llm_error"}`), want: core.TerminationReasonLLMError},
		{name: "cancelled nil payload defaults to cancelled", typ: core.EventRunCancelled, payload: nil, want: core.TerminationReasonCancelled},
		{name: "cancelled carries emitter reason", typ: core.EventRunCancelled, payload: json.RawMessage(`{"termination_reason":"cancelled"}`), want: core.TerminationReasonCancelled},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw := core.BuildLifecyclePayload(tc.typ, tc.payload, corr)
			var payload core.RunTerminalPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.TerminationReason != tc.want {
				t.Fatalf("TerminationReason=%q want %q", payload.TerminationReason, tc.want)
			}
		})
	}
}
