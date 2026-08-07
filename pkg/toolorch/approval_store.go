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

// RunStateExporter is an optional ApprovalStore capability: the runtime
// persists the run's cached decisions into pause checkpoints so a resume on
// another node (pause/resume to a different worker, crash recovery by the
// reaper) keeps the "remembered" approvals instead of re-prompting. Stores
// that do not implement it degrade to the previous behavior: decisions stay
// process-local and are lost when the run migrates.
type RunStateExporter interface {
	// ExportRun serializes every cached decision of runID. The boolean is
	// false when the run has no decisions worth persisting.
	ExportRun(runID string) (json.RawMessage, bool)
	// ImportRun replaces the run's cached decisions with checkpointed state
	// produced by ExportRun (the checkpoint is the durable truth).
	ImportRun(runID string, data json.RawMessage) error
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

// ExportRun implements RunStateExporter: the run's decisions as a JSON object
// keyed by ApprovalKey.
func (s *MemoryApprovalStore) ExportRun(runID string) (json.RawMessage, bool) {
	if s == nil || runID == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.byRun[runID]
	if len(m) == 0 {
		return nil, false
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// ImportRun implements RunStateExporter. Entries that are neither allow nor
// deny are dropped, matching Put's durability contract.
func (s *MemoryApprovalStore) ImportRun(runID string, data json.RawMessage) error {
	if s == nil || runID == "" {
		return nil
	}
	var decoded map[ApprovalKey]Decision
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byRun == nil {
		s.byRun = make(map[string]map[ApprovalKey]Decision)
	}
	m := make(map[ApprovalKey]Decision, len(decoded))
	for key, decision := range decoded {
		if decision != DecisionAllow && decision != DecisionDeny {
			continue
		}
		m[key] = decision
	}
	s.byRun[runID] = m
	return nil
}
