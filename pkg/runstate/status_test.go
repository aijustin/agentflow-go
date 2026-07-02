package runstate_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRunStatusValidAndTransitions(t *testing.T) {
	if !runstate.RunStatusRunning.Valid() {
		t.Fatal("running should be valid")
	}
	if runstate.RunStatus("bogus").Valid() {
		t.Fatal("bogus status should be invalid")
	}
	if runstate.RunStatusPaused.CanTransitionTo(runstate.RunStatusRunning) {
		// ok
	} else {
		t.Fatal("paused should resume to running")
	}
	if runstate.RunStatusFailed.CanTransitionTo(runstate.RunStatusRunning) {
		t.Fatal("failed should not transition to running without override")
	}
}

func TestValidateStatusTransition(t *testing.T) {
	prev := &runstate.RunSnapshot{Status: runstate.RunStatusRunning}
	if err := runstate.ValidateStatusTransition(context.Background(), prev, runstate.RunStatusCompleted); err != nil {
		t.Fatalf("expected valid transition: %v", err)
	}
	if err := runstate.ValidateStatusTransition(context.Background(), prev, runstate.RunStatus("bogus")); err == nil {
		t.Fatal("expected invalid status error")
	}
	if err := runstate.ValidateStatusTransition(context.Background(), prev, runstate.RunStatusPaused); err != nil {
		t.Fatal(err)
	}
	if err := runstate.ValidateStatusTransition(context.Background(), &runstate.RunSnapshot{Status: runstate.RunStatusCompleted}, runstate.RunStatusRunning); err == nil {
		t.Fatal("expected invalid transition from completed")
	}
}

func TestValidateStatusTransitionOverride(t *testing.T) {
	ctx := runstate.ContextWithStatusTransitionOverride(context.Background())
	prev := &runstate.RunSnapshot{Status: runstate.RunStatusCompleted}
	if err := runstate.ValidateStatusTransition(ctx, prev, runstate.RunStatusRunning); err != nil {
		t.Fatalf("override should allow reopen: %v", err)
	}
	if !runstate.StatusTransitionOverrideFromContext(ctx) {
		t.Fatal("expected override flag")
	}
}

func TestRunSnapshotValidate(t *testing.T) {
	if err := (&runstate.RunSnapshot{RunID: "r1", Status: runstate.RunStatusRunning}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&runstate.RunSnapshot{Status: runstate.RunStatusRunning}).Validate(); err == nil {
		t.Fatal("expected missing run_id error")
	}
	if err := (&runstate.RunSnapshot{RunID: "r1", Status: "bogus"}).Validate(); err == nil {
		t.Fatal("expected invalid status error")
	}
}

func TestLoadStepOutputRequiresBlobStore(t *testing.T) {
	_, err := runstate.LoadStepOutput(context.Background(), nil, runstate.StepOutputRef{
		Blob: &runstate.BlobRef{ID: "abc"},
	})
	if err == nil {
		t.Fatal("expected blob store required error")
	}
}
