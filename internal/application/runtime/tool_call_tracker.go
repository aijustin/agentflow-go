package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// toolCallTracker tracks successful per-tool counts and per-(tool,input)
// attempt counts for governance and rate caps within one autonomous tool loop.
type toolCallTracker struct {
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
	if t.ByName == nil {
		t.ByName = make(map[string]int)
	}
	if t.BySameInput == nil {
		t.BySameInput = make(map[string]int)
	}
	return t
}

func (t *toolCallTracker) nameCount(name string) int {
	t = t.ensure()
	return t.ByName[name]
}

func (t *toolCallTracker) sameInputCount(tool string, input json.RawMessage) int {
	t = t.ensure()
	return t.BySameInput[toolInputFingerprint(tool, input)]
}

func (t *toolCallTracker) recordAttempt(tool string, input json.RawMessage) {
	t = t.ensure()
	fp := toolInputFingerprint(tool, input)
	t.BySameInput[fp]++
}

func (t *toolCallTracker) recordSuccess(name string) {
	t = t.ensure()
	t.ByName[name]++
}

func (t *toolCallTracker) totalSuccesses() int {
	t = t.ensure()
	total := 0
	for _, count := range t.ByName {
		total += count
	}
	return total
}

func toolInputFingerprint(tool string, input json.RawMessage) string {
	return tool + "\x00" + canonicalJSON(input)
}

func canonicalJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
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
