package governance

import (
	"context"
	"encoding/json"
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
