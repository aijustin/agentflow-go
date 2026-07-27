package runtime

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// pathLockKeys are JSON argument fields that identify a filesystem target.
// Matching grok-build's lock_path_for_args: concurrent calls that share a
// path serialize; directory-only keys are intentionally omitted.
var pathLockKeys = []string{"file_path", "path", "target_file"}

// lockPathForArgs returns a normalized path key for serializing concurrent
// tool calls that target the same file. Empty string means no path lock.
func lockPathForArgs(input json.RawMessage) string {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return ""
	}
	for _, key := range pathLockKeys {
		value, ok := args[key]
		if !ok {
			continue
		}
		path, ok := value.(string)
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if path != "" {
			return path
		}
	}
	return ""
}

// governanceLockKey returns the key concurrent calls in a batch must serialize
// on for the configured per-run tool budgets to hold, or "" when the call is
// ungoverned.
//
// The doom-loop limit and the rate cap are both check-then-act on a counter
// shared by the whole batch: without serialization every goroutine reads the
// count before any of them records, so a batch of N identical calls passes a
// gate meant to admit one. The key is chosen to serialize no more than the
// limit requires.
func (e *Engine) governanceLockKey(call llm.ToolCall) string {
	if tool, ok := e.scenario.Tools[call.Name]; ok && tool.RateCap > 0 {
		// A rate cap counts every call to the tool whatever its input, so the
		// count is only correct if the tool as a whole serializes.
		return "tool" + toolInputFingerprintSep + call.Name
	}
	if e.scenario.Runtime.DoomLoopLimit > 0 {
		// The doom-loop limit only counts repeats of one input, so distinct
		// inputs keep running concurrently.
		return "input" + toolInputFingerprintSep + toolInputFingerprint(call.Name, call.Input)
	}
	return ""
}

// keyedLockSet holds per-key mutexes for one tool batch, so calls that share a
// key run one at a time while unrelated calls stay parallel.
type keyedLockSet struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedLockSet() *keyedLockSet {
	return &keyedLockSet{locks: make(map[string]*sync.Mutex)}
}

// acquire locks the key (no-op when key is empty) and returns an unlock func.
func (s *keyedLockSet) acquire(key string) func() {
	if key == "" {
		return func() {}
	}
	s.mu.Lock()
	lock, ok := s.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}
