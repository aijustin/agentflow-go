package slog

import (
	"bytes"
	"context"
	"encoding/json"
	stdslog "log/slog"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestSinkEmitsStructuredRuntimeEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(stdslog.New(stdslog.NewJSONHandler(&buf, nil)))
	if err := sink.Emit(context.Background(), core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "scenario", Timestamp: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC), TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["event_type"] != string(core.EventRunStarted) || got["run_id"] != "run-1" || got["scenario_name"] != "scenario" || got["trace_id"] != "trace-1" {
		t.Fatalf("unexpected log fields: %+v", got)
	}
	if _, ok := got["payload"]; ok {
		t.Fatalf("payload should be omitted by default: %+v", got)
	}
}

func TestSinkCanIncludePayload(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(stdslog.New(stdslog.NewJSONHandler(&buf, nil)), WithPayload())
	if err := sink.Emit(context.Background(), core.Event{Type: core.EventRunFailed, RunID: "run-1", Payload: json.RawMessage(`{"error":"boom"}`)}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["payload"] != `{"error":"boom"}` {
		t.Fatalf("expected payload field, got %+v", got)
	}
}
