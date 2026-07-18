package planmode

import "testing"

func TestPlanModeLifecycle(t *testing.T) {
	t.Parallel()
	tr := New("/tmp/session/plan.md")
	if !tr.EnterPending() || tr.State() != StatePending {
		t.Fatalf("enter pending: %s", tr.State())
	}
	if !tr.Activate() || !tr.IsActive() {
		t.Fatal("expected active")
	}
	if !tr.ShouldAutoApproveEdit("/tmp/session/plan.md") {
		t.Fatal("plan file edit should auto-approve")
	}
	if tr.AllowsWriteTool("/tmp/other.go") {
		t.Fatal("other writes blocked in active plan mode")
	}
	if !tr.RequestExit(true) || tr.State() != StateExitPending {
		t.Fatalf("expected exit_pending, got %s", tr.State())
	}
	if !tr.CompleteExit() || tr.State() != StateInactive {
		t.Fatalf("expected inactive after complete exit, got %s", tr.State())
	}
	if !tr.HasPendingExitReminder() {
		t.Fatal("expected pending exit reminder")
	}
}

func TestFromSnapshotCollapsesTransient(t *testing.T) {
	t.Parallel()
	tr := FromSnapshot("/p/plan.md", Snapshot{State: StatePending, WasPreviouslyActive: true})
	if tr.State() != StateInactive {
		t.Fatalf("pending should collapse to inactive, got %s", tr.State())
	}
	tr = FromSnapshot("/p/plan.md", Snapshot{State: StateExitPending})
	if tr.State() != StateInactive || !tr.HasPendingExitReminder() {
		t.Fatalf("exit_pending collapse: state=%s reminder=%v", tr.State(), tr.HasPendingExitReminder())
	}
}
