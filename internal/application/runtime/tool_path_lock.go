package runtime

import (
	"encoding/json"
	"strings"
	"sync"
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

// pathLockSet holds per-path mutexes for one tool batch.
type pathLockSet struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newPathLockSet() *pathLockSet {
	return &pathLockSet{locks: make(map[string]*sync.Mutex)}
}

// acquire locks the path (no-op when path is empty) and returns an unlock func.
func (s *pathLockSet) acquire(path string) func() {
	if path == "" {
		return func() {}
	}
	s.mu.Lock()
	lock, ok := s.locks[path]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[path] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}
