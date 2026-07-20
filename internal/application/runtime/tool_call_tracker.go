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
	mu          sync.Mutex
	ByName      map[string]int `json:"by_name"`
	BySameInput map[string]int `json:"by_same_input"`
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{
		ByName:      make(map[string]int),
		BySameInput: make(map[string]int),
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
