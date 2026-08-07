package toolorch

import (
	"fmt"
	"sync"
)

// DenyBreaker tracks consecutive HITL/tool approval denials per run and trips
// when the limit is reached. Orthogonal to doom_loop_limit (tool input thrash).
type DenyBreaker struct {
	mu    sync.Mutex
	limit int
	count map[string]int
}

// NewDenyBreaker creates a breaker. Limit <= 0 disables tripping.
func NewDenyBreaker(limit int) *DenyBreaker {
	return &DenyBreaker{limit: limit, count: make(map[string]int)}
}

// Limit returns the configured consecutive-deny threshold.
func (b *DenyBreaker) Limit() int {
	if b == nil {
		return 0
	}
	return b.limit
}

// RecordDeny increments the consecutive deny counter. Returns true when tripped.
func (b *DenyBreaker) RecordDeny(runID string) (tripped bool, count int) {
	if b == nil || b.limit <= 0 || runID == "" {
		return false, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == nil {
		b.count = make(map[string]int)
	}
	b.count[runID]++
	count = b.count[runID]
	return count >= b.limit, count
}

// RecordAllow resets the consecutive deny counter after a successful approval path.
func (b *DenyBreaker) RecordAllow(runID string) {
	if b == nil || runID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.count, runID)
}

// Clear drops state for runID.
func (b *DenyBreaker) Clear(runID string) {
	b.RecordAllow(runID)
}

// ExportRun returns the run's current consecutive-deny count (0 when none).
// The runtime persists it into pause checkpoints so the breaker survives a
// run migrating to another node.
func (b *DenyBreaker) ExportRun(runID string) int {
	if b == nil || runID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count[runID]
}

// ImportRun restores a checkpointed consecutive-deny count for runID,
// replacing any in-process value (the checkpoint is the durable truth). A
// count <= 0 clears the run's state.
func (b *DenyBreaker) ImportRun(runID string, count int) {
	if b == nil || runID == "" {
		return
	}
	if count <= 0 {
		b.RecordAllow(runID)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == nil {
		b.count = make(map[string]int)
	}
	b.count[runID] = count
}

// TripError formats the fail-closed error when the breaker trips.
func TripError(limit, count int) error {
	return fmt.Errorf("runtime: HITL deny breaker tripped after %d consecutive denials (limit=%d)", count, limit)
}
