package toolorch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
)

// Decision is a cached approval outcome for a tool invocation key.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	// DecisionPause means human approval is still required (not cached as allow).
	DecisionPause Decision = "pause"
)

// ApprovalKey uniquely identifies a tool+input pair within a run/session.
type ApprovalKey string

// Key builds a stable approval key from tool name and canonical JSON input.
func Key(tool string, input json.RawMessage) ApprovalKey {
	tool = strings.TrimSpace(tool)
	sum := sha256.Sum256(append([]byte(tool+"\x00"), canonicalJSON(input)...))
	return ApprovalKey(hex.EncodeToString(sum[:]))
}

func canonicalJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return append([]byte(nil), raw...)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return append([]byte(nil), raw...)
	}
	return encoded
}

// ApprovalStore caches allow/deny decisions so repeated HITL prompts for the
// same key can be skipped within a run.
type ApprovalStore interface {
	Get(runID string, key ApprovalKey) (Decision, bool)
	Put(runID string, key ApprovalKey, decision Decision)
	Clear(runID string)
}

// MemoryApprovalStore is an in-process ApprovalStore.
type MemoryApprovalStore struct {
	mu    sync.Mutex
	byRun map[string]map[ApprovalKey]Decision
}

// NewMemoryApprovalStore creates an empty store.
func NewMemoryApprovalStore() *MemoryApprovalStore {
	return &MemoryApprovalStore{byRun: make(map[string]map[ApprovalKey]Decision)}
}

// Get returns a cached decision.
func (s *MemoryApprovalStore) Get(runID string, key ApprovalKey) (Decision, bool) {
	if s == nil || runID == "" || key == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.byRun[runID]
	if m == nil {
		return "", false
	}
	d, ok := m[key]
	return d, ok
}

// Put caches a decision. DecisionPause is ignored (not durable).
func (s *MemoryApprovalStore) Put(runID string, key ApprovalKey, decision Decision) {
	if s == nil || runID == "" || key == "" {
		return
	}
	if decision != DecisionAllow && decision != DecisionDeny {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byRun == nil {
		s.byRun = make(map[string]map[ApprovalKey]Decision)
	}
	m := s.byRun[runID]
	if m == nil {
		m = make(map[ApprovalKey]Decision)
		s.byRun[runID] = m
	}
	m[key] = decision
}

// Clear drops all cached decisions for runID.
func (s *MemoryApprovalStore) Clear(runID string) {
	if s == nil || runID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byRun, runID)
}
