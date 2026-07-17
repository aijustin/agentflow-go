package runtime

import (
	"encoding/json"
	"testing"
)

func TestToolInputFingerprintCanonicalizesJSON(t *testing.T) {
	t.Parallel()
	a := toolInputFingerprint("box", json.RawMessage(`{"date":"2026-07-01","page":1}`))
	b := toolInputFingerprint("box", json.RawMessage(`{"page":1,"date":"2026-07-01"}`))
	if a != b {
		t.Fatalf("expected canonical fingerprint match, got %q vs %q", a, b)
	}
	c := toolInputFingerprint("box", json.RawMessage(`{"date":"2026-07-02","page":1}`))
	if a == c {
		t.Fatal("different inputs must not share fingerprint")
	}
}

func TestToolCallTrackerRecordsAttemptsAndSuccesses(t *testing.T) {
	t.Parallel()
	tracker := newToolCallTracker()
	input := json.RawMessage(`{"date":"2026-07-01"}`)
	if got := tracker.sameInputCount("box", input); got != 0 {
		t.Fatalf("sameInputCount=%d want 0", got)
	}
	tracker.recordAttempt("box", input)
	tracker.recordSuccess("box")
	tracker.recordAttempt("box", input)
	if got := tracker.sameInputCount("box", input); got != 2 {
		t.Fatalf("sameInputCount=%d want 2", got)
	}
	if got := tracker.nameCount("box"); got != 1 {
		t.Fatalf("nameCount=%d want 1", got)
	}
	if got := tracker.totalSuccesses(); got != 1 {
		t.Fatalf("totalSuccesses=%d want 1", got)
	}
}

func TestDecodeToolCallTrackerLegacyAndNew(t *testing.T) {
	t.Parallel()
	legacy, err := decodeToolCallTracker(json.RawMessage(`{"echo":2,"other":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.nameCount("echo") != 2 || legacy.nameCount("other") != 1 {
		t.Fatalf("legacy decode failed: %+v", legacy)
	}
	if len(legacy.BySameInput) != 0 {
		t.Fatalf("legacy decode should leave same-input empty, got %+v", legacy.BySameInput)
	}

	modern, err := decodeToolCallTracker(json.RawMessage(`{"by_name":{"echo":3},"by_same_input":{"echo\u0000{}":4}}`))
	if err != nil {
		t.Fatal(err)
	}
	if modern.nameCount("echo") != 3 {
		t.Fatalf("by_name decode failed: %+v", modern)
	}
	if modern.BySameInput["echo\x00{}"] != 4 {
		t.Fatalf("by_same_input decode failed: %+v", modern.BySameInput)
	}
}

func TestGovernanceBlockErrorStripsDeniedPrefix(t *testing.T) {
	t.Parallel()
	got := governanceBlockError(errString("governance: denied: run_tool_loop_guard: tool=x repeated=3 limit=3"))
	want := "tool invocation blocked by governance: run_tool_loop_guard: tool=x repeated=3 limit=3"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
