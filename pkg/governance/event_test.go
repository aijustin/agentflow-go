package governance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestRedactEventPayload(t *testing.T) {
	redactor := NewJSONFieldRedactor("secret")
	payload := json.RawMessage(`{"secret":"value","ok":true}`)
	out := RedactEventPayload(context.Background(), redactor, "run-1", core.EventStepCompleted, payload)
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["secret"] != "[REDACTED]" || decoded["ok"] != true {
		t.Fatalf("unexpected redacted payload: %+v", decoded)
	}
}

func TestRedactEventPayloadPassthrough(t *testing.T) {
	payload := json.RawMessage(`{"ok":true}`)
	if got := RedactEventPayload(context.Background(), nil, "run-1", core.EventRunStarted, payload); string(got) != string(payload) {
		t.Fatalf("expected passthrough, got %s", got)
	}
	if got := RedactEventPayload(context.Background(), NewJSONFieldRedactor(), "run-1", core.EventRunStarted, nil); got != nil {
		t.Fatalf("expected nil payload passthrough, got %s", got)
	}
}

type failingRedactor struct{}

func (failingRedactor) RedactOutput(context.Context, OutputRedaction) (json.RawMessage, error) {
	return nil, errors.New("redact failed")
}

func TestRedactEventPayloadKeepsOriginalOnFailure(t *testing.T) {
	payload := json.RawMessage(`{"secret":"keep"}`)
	out := RedactEventPayload(context.Background(), failingRedactor{}, "run-1", core.EventStepCompleted, payload)
	if string(out) != string(payload) {
		t.Fatalf("expected original payload on redaction failure, got %s", out)
	}
}
