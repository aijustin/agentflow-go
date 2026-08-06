package runtime

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
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

func TestToolInputFingerprintIsPostgresJSONBSafe(t *testing.T) {
	t.Parallel()
	tracker := newToolCallTracker()
	tracker.recordAttempt("mcp_list_login_cinemas", json.RawMessage(`{}`))
	fp := toolInputFingerprint("mcp_list_login_cinemas", json.RawMessage(`{}`))
	if strings.ContainsRune(fp, 0) {
		t.Fatalf("fingerprint must not contain NUL (Postgres jsonb rejects \\u0000): %q", fp)
	}
	if !strings.Contains(fp, toolInputFingerprintSep) {
		t.Fatalf("fingerprint missing separator: %q", fp)
	}
	raw, err := json.Marshal(tracker)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		BySameInput map[string]int `json:"by_same_input"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for key := range wire.BySameInput {
		if strings.ContainsRune(key, 0) {
			t.Fatalf("by_same_input key must not contain NUL: %q", key)
		}
	}
	if wire.BySameInput[fp] != 1 {
		t.Fatalf("by_same_input[%q]=%d want 1 (raw=%s)", fp, wire.BySameInput[fp], raw)
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

	modernKey := "echo" + toolInputFingerprintSep + "{}"
	// JSON string literals must escape RS as \u001e (raw 0x1E is illegal in JSON).
	modern, err := decodeToolCallTracker(json.RawMessage(
		`{"by_name":{"echo":3},"by_same_input":{"echo\u001e{}":4}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if modern.nameCount("echo") != 3 {
		t.Fatalf("by_name decode failed: %+v", modern)
	}
	if modern.BySameInput[modernKey] != 4 {
		t.Fatalf("by_same_input decode failed: %+v", modern.BySameInput)
	}
}

// DEFECT_REPORT D1: concurrent reservations against one tracker must admit
// exactly the budgeted number of callers — the check and the increment share
// one lock, so a parallel batch cannot overrun the cap.
func TestToolCallTrackerConcurrentReservations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		doomLoopLimit int
		rateCap       int
		wantAdmitted  int
	}{
		{name: "rate cap admits exactly k", rateCap: 3, wantAdmitted: 3},
		// The doom-loop check fires on the limit-th repetition, so a limit of
		// 3 admits exactly 2 concurrent same-input calls.
		{name: "doom loop admits exactly limit-1", doomLoopLimit: 3, wantAdmitted: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tracker := newToolCallTracker()
			input := json.RawMessage(`{"query":"loop"}`)
			const callers = 16
			var admitted atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for range callers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					reservation, _, denial := tracker.reserveToolCall("echo", input, tc.doomLoopLimit, tc.rateCap)
					if denial != "" {
						return
					}
					admitted.Add(1)
					reservation.commitSuccess()
				}()
			}
			close(start)
			wg.Wait()
			if got := admitted.Load(); got != int32(tc.wantAdmitted) {
				t.Fatalf("admitted=%d want %d", got, tc.wantAdmitted)
			}
			// Once the admitted callers have committed, the budget stays
			// exhausted: the next caller must be denied.
			if _, _, denial := tracker.reserveToolCall("echo", input, tc.doomLoopLimit, tc.rateCap); denial == "" {
				t.Fatal("expected denial after the committed budget is spent")
			}
		})
	}
}

// DEFECT_REPORT D1: the governance-facing counts captured at reservation
// time must include in-flight siblings, otherwise N concurrent calls each
// observe the same committed-only counts and collectively overrun a
// governance budget (e.g. NewToolBudgetPolicy).
func TestToolCallTrackerCountsIncludeInFlightSiblings(t *testing.T) {
	t.Parallel()
	tracker := newToolCallTracker()
	input := json.RawMessage(`{"query":"a"}`)
	first, counts, denial := tracker.reserveToolCall("echo", input, 0, 0)
	if denial != "" {
		t.Fatalf("unexpected denial: %s", denial)
	}
	if counts.byName != 0 || counts.bySameInput != 0 || counts.total != 0 {
		t.Fatalf("first call must see zero prior counts, got %+v", counts)
	}
	// While the first call is still in flight, a concurrent sibling must
	// observe it in every count dimension.
	second, counts, denial := tracker.reserveToolCall("echo", input, 0, 0)
	if denial != "" {
		t.Fatalf("unexpected denial: %s", denial)
	}
	if counts.byName != 1 || counts.bySameInput != 1 || counts.total != 1 {
		t.Fatalf("sibling must observe one in-flight call, got %+v", counts)
	}
	first.commitSuccess()
	// After the first commits, a new caller sees committed + in-flight.
	third, counts, denial := tracker.reserveToolCall("echo", input, 0, 0)
	if denial != "" {
		t.Fatalf("unexpected denial: %s", denial)
	}
	if counts.byName != 2 || counts.bySameInput != 2 || counts.total != 2 {
		t.Fatalf("caller must observe committed plus in-flight, got %+v", counts)
	}
	second.commitSuccess()
	third.commitSuccess()
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
