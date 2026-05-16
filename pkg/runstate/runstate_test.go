package runstate

import "testing"

func TestRunStatusTransitions(t *testing.T) {
	if !RunStatusRunning.CanTransitionTo(RunStatusPaused) {
		t.Fatal("running should transition to paused")
	}
	if RunStatusCompleted.CanTransitionTo(RunStatusRunning) {
		t.Fatal("completed must be terminal")
	}
}
