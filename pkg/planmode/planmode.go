// Package planmode provides a pure interactive plan-mode state machine.
// It has no I/O; hosts own persistence, prompts, and tool gating.
package planmode

import "path/filepath"

// State is the plan-mode lifecycle state.
type State string

const (
	StateInactive    State = "inactive"
	StatePending     State = "pending"
	StateActive      State = "active"
	StateExitPending State = "exit_pending"
)

// Snapshot is a persistable view of plan-mode lifecycle state.
type Snapshot struct {
	State                State `json:"state"`
	WasPreviouslyActive  bool  `json:"was_previously_active"`
	ReminderCount        uint32 `json:"reminder_count"`
	PendingExitReminder  bool  `json:"pending_exit_reminder"`
	AwaitingPlanApproval bool  `json:"awaiting_plan_approval"`
}

// Tracker manages plan-mode transitions without SessionActor / I/O deps.
type Tracker struct {
	state                State
	wasPreviouslyActive  bool
	reminderCount        uint32
	pendingExitReminder  bool
	awaitingPlanApproval bool
	planFilePath         string
}

// New creates a tracker. planFilePath is the absolute plan.md path for the session.
func New(planFilePath string) *Tracker {
	return &Tracker{
		state:        StateInactive,
		planFilePath: planFilePath,
	}
}

// FromSnapshot restores a tracker. Transient Pending/ExitPending collapse on
// restart: Pending→Inactive, ExitPending→Inactive with exit reminder.
func FromSnapshot(planFilePath string, snap Snapshot) *Tracker {
	switch snap.State {
	case StatePending:
		snap.State = StateInactive
	case StateExitPending:
		snap.State = StateInactive
		snap.PendingExitReminder = true
	}
	return &Tracker{
		state:                snap.State,
		wasPreviouslyActive:  snap.WasPreviouslyActive,
		reminderCount:        snap.ReminderCount,
		pendingExitReminder:  snap.PendingExitReminder,
		awaitingPlanApproval: snap.AwaitingPlanApproval,
		planFilePath:         planFilePath,
	}
}

func (t *Tracker) Snapshot() Snapshot {
	return Snapshot{
		State:                t.state,
		WasPreviouslyActive:  t.wasPreviouslyActive,
		ReminderCount:        t.reminderCount,
		PendingExitReminder:  t.pendingExitReminder,
		AwaitingPlanApproval: t.awaitingPlanApproval,
	}
}

func (t *Tracker) State() State { return t.state }

func (t *Tracker) IsActive() bool { return t.state == StateActive }

func (t *Tracker) PlanFilePath() string { return t.planFilePath }

func (t *Tracker) SetAwaitingPlanApproval(awaiting bool) {
	t.awaitingPlanApproval = awaiting
}

func (t *Tracker) IsAwaitingPlanApproval() bool { return t.awaitingPlanApproval }

func (t *Tracker) HasPendingExitReminder() bool { return t.pendingExitReminder }

func (t *Tracker) ClearPendingExitReminder() { t.pendingExitReminder = false }

func (t *Tracker) ShouldUseFullReminder() bool { return t.reminderCount%2 == 0 }

func (t *Tracker) IncrementReminderCount() { t.reminderCount++ }

// ShouldAutoApproveEdit reports whether an edit path targets the plan file
// while plan mode is active (hosts may skip write approval for that file).
func (t *Tracker) ShouldAutoApproveEdit(editPath string) bool {
	if !t.IsActive() || t.planFilePath == "" || editPath == "" {
		return false
	}
	return filepath.Clean(editPath) == filepath.Clean(t.planFilePath)
}

// AllowsWriteTool reports whether a write-side-effect tool may run.
// In Active plan mode only the plan-file write path is allowed; other writes
// must be blocked by the host.
func (t *Tracker) AllowsWriteTool(editPath string) bool {
	if !t.IsActive() {
		return true
	}
	return t.ShouldAutoApproveEdit(editPath)
}

// EnterPending is the client toggle ON. Returns whether state changed.
func (t *Tracker) EnterPending() bool {
	switch t.state {
	case StateInactive:
		t.state = StatePending
		t.pendingExitReminder = false
		return true
	case StateExitPending:
		t.state = StateActive
		t.pendingExitReminder = false
		return true
	default:
		return false
	}
}

// Activate transitions Pending → Active on the first user prompt.
func (t *Tracker) Activate() bool {
	if t.state != StatePending {
		return false
	}
	t.state = StateActive
	t.wasPreviouslyActive = true
	t.reminderCount = 0
	return true
}

// RequestExit is the client toggle OFF.
// Idle Active → Inactive; in-flight Active → ExitPending.
func (t *Tracker) RequestExit(turnInFlight bool) bool {
	switch t.state {
	case StatePending:
		t.state = StateInactive
		return true
	case StateActive:
		if turnInFlight {
			t.state = StateExitPending
			return true
		}
		t.state = StateInactive
		t.pendingExitReminder = true
		t.awaitingPlanApproval = false
		return true
	case StateExitPending:
		return false
	default:
		return false
	}
}

// CompleteExit finishes ExitPending after the in-flight turn ends.
func (t *Tracker) CompleteExit() bool {
	if t.state != StateExitPending {
		return false
	}
	t.state = StateInactive
	t.pendingExitReminder = true
	t.awaitingPlanApproval = false
	return true
}

// ApproveExit completes an approved exit_plan_mode while Active.
func (t *Tracker) ApproveExit() bool {
	if t.state != StateActive && t.state != StateExitPending {
		return false
	}
	t.state = StateInactive
	t.pendingExitReminder = true
	t.awaitingPlanApproval = false
	return true
}
