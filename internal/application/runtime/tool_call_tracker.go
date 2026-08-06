package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
)

// toolCallTracker tracks successful per-tool counts and per-(tool,input)
// attempt counts for governance and rate caps within one autonomous tool loop.
type toolCallTracker struct {
	mu                  sync.Mutex
	ByName              map[string]int `json:"by_name"`
	BySameInput         map[string]int `json:"by_same_input"`
	reservedByName      map[string]int
	reservedBySameInput map[string]int
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{
		ByName:              make(map[string]int),
		BySameInput:         make(map[string]int),
		reservedByName:      make(map[string]int),
		reservedBySameInput: make(map[string]int),
	}
}

func (t *toolCallTracker) ensure() *toolCallTracker {
	if t == nil {
		return newToolCallTracker()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	return t
}

func (t *toolCallTracker) initMapsLocked() {
	if t.ByName == nil {
		t.ByName = make(map[string]int)
	}
	if t.BySameInput == nil {
		t.BySameInput = make(map[string]int)
	}
	if t.reservedByName == nil {
		t.reservedByName = make(map[string]int)
	}
	if t.reservedBySameInput == nil {
		t.reservedBySameInput = make(map[string]int)
	}
}

type toolCallReservation struct {
	tracker     *toolCallTracker
	name        string
	fingerprint string
	active      bool
}

// toolCallCounts is the governance-facing budget view captured under the
// tracker lock at reservation time: committed counts plus the in-flight
// reservations of sibling calls in a parallel batch (excluding the reserving
// call itself). A committed-only reading would let N concurrent calls each
// observe the same pre-batch counts and collectively overrun a governance
// budget; this view makes the budget check as atomic as the reservation.
type toolCallCounts struct {
	byName      int
	bySameInput int
	total       int
}

// reserveToolCall atomically checks and reserves the per-tool execution
// budget. Reservations account for in-flight sibling calls in a parallel
// batch, while committed maps preserve the checkpoint format and its
// historical semantics (successful calls by name, all attempts by input).
func (t *toolCallTracker) reserveToolCall(name string, input json.RawMessage, doomLoopLimit, rateCap int) (toolCallReservation, toolCallCounts, string) {
	t = t.ensure()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()

	fingerprint := toolInputFingerprint(name, input)
	if doomLoopLimit > 0 && t.BySameInput[fingerprint]+t.reservedBySameInput[fingerprint]+1 >= doomLoopLimit {
		return toolCallReservation{}, toolCallCounts{}, formatDoomLoopError(name, doomLoopLimit)
	}
	if rateCap > 0 && t.ByName[name]+t.reservedByName[name] >= rateCap {
		return toolCallReservation{}, toolCallCounts{}, fmt.Sprintf("tool rate cap exceeded: %d call(s) per run", rateCap)
	}
	t.reservedByName[name]++
	t.reservedBySameInput[fingerprint]++
	return toolCallReservation{
		tracker:     t,
		name:        name,
		fingerprint: fingerprint,
		active:      true,
	}, t.countsLocked(name, fingerprint), ""
}

// countsLocked computes the governance budget view for a call that has just
// been reserved (its own reservation is already in the maps, hence the -1:
// the view reports prior committed plus *other* in-flight calls). Must be
// called with t.mu held.
func (t *toolCallTracker) countsLocked(name, fingerprint string) toolCallCounts {
	totalCommitted := 0
	for _, count := range t.ByName {
		totalCommitted += count
	}
	totalReserved := 0
	for _, count := range t.reservedByName {
		totalReserved += count
	}
	return toolCallCounts{
		byName:      t.ByName[name] + t.reservedByName[name] - 1,
		bySameInput: t.BySameInput[fingerprint] + t.reservedBySameInput[fingerprint] - 1,
		total:       totalCommitted + totalReserved - 1,
	}
}

func (r *toolCallReservation) release() {
	if r == nil || !r.active || r.tracker == nil {
		return
	}
	r.tracker.mu.Lock()
	defer r.tracker.mu.Unlock()
	r.tracker.initMapsLocked()
	r.tracker.reservedByName[r.name]--
	r.tracker.reservedBySameInput[r.fingerprint]--
	r.active = false
}

// Release implements toolinspect.Reservation for the inspector pipeline.
func (r *toolCallReservation) Release() { r.release() }

// CommitAttempt implements toolinspect.Reservation for the inspector pipeline.
func (r *toolCallReservation) CommitAttempt() { r.commitAttempt() }

// CommitSuccess implements toolinspect.Reservation for the inspector pipeline.
func (r *toolCallReservation) CommitSuccess() { r.commitSuccess() }

func (r *toolCallReservation) commitAttempt() {
	if r == nil || !r.active || r.tracker == nil {
		return
	}
	r.tracker.mu.Lock()
	defer r.tracker.mu.Unlock()
	r.tracker.initMapsLocked()
	r.tracker.reservedByName[r.name]--
	r.tracker.reservedBySameInput[r.fingerprint]--
	r.tracker.BySameInput[r.fingerprint]++
	r.active = false
}

func (r *toolCallReservation) commitSuccess() {
	if r == nil || !r.active || r.tracker == nil {
		return
	}
	r.tracker.mu.Lock()
	defer r.tracker.mu.Unlock()
	r.tracker.initMapsLocked()
	r.tracker.reservedByName[r.name]--
	r.tracker.reservedBySameInput[r.fingerprint]--
	r.tracker.BySameInput[r.fingerprint]++
	r.tracker.ByName[r.name]++
	r.active = false
}

func (t *toolCallTracker) nameCount(name string) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	return t.ByName[name]
}

func (t *toolCallTracker) sameInputCount(tool string, input json.RawMessage) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	return t.BySameInput[toolInputFingerprint(tool, input)]
}

func (t *toolCallTracker) recordAttempt(tool string, input json.RawMessage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	fp := toolInputFingerprint(tool, input)
	t.BySameInput[fp]++
}

func (t *toolCallTracker) recordSuccess(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	t.ByName[name]++
}

func (t *toolCallTracker) totalSuccesses() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initMapsLocked()
	total := 0
	for _, count := range t.ByName {
		total += count
	}
	return total
}

// MarshalJSON exports tracker counts without the mutex.
func (t *toolCallTracker) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte(`{"by_name":{},"by_same_input":{}}`), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	type wire struct {
		ByName      map[string]int `json:"by_name"`
		BySameInput map[string]int `json:"by_same_input"`
	}
	return json.Marshal(wire{ByName: t.ByName, BySameInput: t.BySameInput})
}

// toolInputFingerprintSep separates tool name from canonical input JSON in
// by_same_input map keys. It must not be NUL (\x00): encoding/json emits
// \u0000 for that byte, and PostgreSQL jsonb rejects \u0000 (SQLSTATE 22P05).
const toolInputFingerprintSep = "\x1e"

func toolInputFingerprint(tool string, input json.RawMessage) string {
	return tool + toolInputFingerprintSep + canonicalJSON(input)
}

func canonicalJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	// Fast path: already compact scalar / empty. Objects still need a
	// round-trip so map key order is stable across producers.
	if len(trimmed) > 0 && trimmed[0] != '{' && trimmed[0] != '[' {
		var buf bytes.Buffer
		if err := json.Compact(&buf, trimmed); err == nil {
			return buf.String()
		}
		return string(trimmed)
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return string(trimmed)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return string(trimmed)
	}
	return string(canonical)
}

// decodeToolCallTracker accepts the current checkpoint shape
// {"by_name":...,"by_same_input":...} and the legacy map[string]int shape
// used before SameInputCalls tracking existed.
func decodeToolCallTracker(raw json.RawMessage) (*toolCallTracker, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return newToolCallTracker(), nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("decode tool call tracker: %w", err)
	}
	if _, hasByName := probe["by_name"]; hasByName {
		var tracker toolCallTracker
		if err := json.Unmarshal(raw, &tracker); err != nil {
			return nil, fmt.Errorf("decode tool call tracker: %w", err)
		}
		return tracker.ensure(), nil
	}
	if _, hasBySame := probe["by_same_input"]; hasBySame {
		var tracker toolCallTracker
		if err := json.Unmarshal(raw, &tracker); err != nil {
			return nil, fmt.Errorf("decode tool call tracker: %w", err)
		}
		return tracker.ensure(), nil
	}
	var byName map[string]int
	if err := json.Unmarshal(raw, &byName); err != nil {
		return nil, fmt.Errorf("decode legacy tool call counts: %w", err)
	}
	tracker := newToolCallTracker()
	for name, count := range byName {
		tracker.ByName[name] = count
	}
	return tracker, nil
}
