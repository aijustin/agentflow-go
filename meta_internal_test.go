package agentflow

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestQuoteJSONStringEscapesForJSON pins D3 for the framework facade: the
// helper must produce JSON string values even for inputs full of control
// characters, quotes, and unicode. strconv.Quote/fmt %q emit Go-only \xNN
// escapes, which are invalid JSON.
func TestQuoteJSONStringEscapesForJSON(t *testing.T) {
	nasty := "quote\" backslash\\ ctrl\x01\x1f\nnewline unicode 中文 emoji 🌀"
	raw := quoteJSONString(nasty)
	if !json.Valid(raw) {
		t.Fatalf("quoteJSONString produced invalid JSON: %q", raw)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != nasty {
		t.Fatalf("round-trip mismatch: got %q want %q", got, nasty)
	}
}

func TestQuoteJSONErrorPayloadEscapesForJSON(t *testing.T) {
	raw := quoteJSONErrorPayload(errors.New("boom\x01\"中文\""))
	if !json.Valid(raw) {
		t.Fatalf("quoteJSONErrorPayload produced invalid JSON: %q", raw)
	}
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "boom\x01\"中文\"" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
