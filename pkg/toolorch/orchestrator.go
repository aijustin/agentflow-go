package toolorch

import (
	"context"
	"encoding/json"
)

// AttemptResult is reported by the host after a tool attempt. Sandbox denial
// is host-owned; the library does not implement OS sandboxing.
type AttemptResult struct {
	DeniedBySandbox bool
	// EscalateRequested asks the orchestrator/host path to re-prompt for a
	// higher-privilege attempt. Default library orchestrator records nothing.
	EscalateRequested bool
}

// ApprovalRequest is the input to DecideApproval.
type ApprovalRequest struct {
	RunID         string
	Tool          string
	Input         json.RawMessage
	PauseRequired bool
}

// ToolOrchestrator unifies approval cache lookup before execute and optional
// post-attempt hooks. Nil orchestrator means the runtime uses built-in HITL only.
type ToolOrchestrator interface {
	DecideApproval(ctx context.Context, req ApprovalRequest) (Decision, error)
	AfterAttempt(ctx context.Context, runID, tool string, input json.RawMessage, result AttemptResult) error
}

// StoreOrchestrator is the default library orchestrator: approval cache only.
type StoreOrchestrator struct {
	Store ApprovalStore
}

// NewStoreOrchestrator wraps an ApprovalStore.
func NewStoreOrchestrator(store ApprovalStore) *StoreOrchestrator {
	if store == nil {
		store = NewMemoryApprovalStore()
	}
	return &StoreOrchestrator{Store: store}
}

// DecideApproval returns a cached allow/deny, or DecisionPause when HITL is still required.
func (o *StoreOrchestrator) DecideApproval(ctx context.Context, req ApprovalRequest) (Decision, error) {
	_ = ctx
	if o == nil || o.Store == nil {
		if req.PauseRequired {
			return DecisionPause, nil
		}
		return DecisionAllow, nil
	}
	key := Key(req.Tool, req.Input)
	if cached, ok := o.Store.Get(req.RunID, key); ok {
		return cached, nil
	}
	if req.PauseRequired {
		return DecisionPause, nil
	}
	return DecisionAllow, nil
}

// AfterAttempt is a no-op for the store-backed orchestrator (sandbox is host-owned).
func (o *StoreOrchestrator) AfterAttempt(ctx context.Context, runID, tool string, input json.RawMessage, result AttemptResult) error {
	_ = ctx
	_ = runID
	_ = tool
	_ = input
	_ = result
	return nil
}

// RememberAllow caches an allow decision after successful human approval.
func RememberAllow(store ApprovalStore, runID, tool string, input json.RawMessage) {
	if store == nil {
		return
	}
	store.Put(runID, Key(tool, input), DecisionAllow)
}

// RememberDeny caches a deny decision after human rejection.
func RememberDeny(store ApprovalStore, runID, tool string, input json.RawMessage) {
	if store == nil {
		return
	}
	store.Put(runID, Key(tool, input), DecisionDeny)
}
