package core

import "testing"

func TestToolApprovalDenialReason(t *testing.T) {
	if reason := ToolApprovalDenialReason(Tool{Approval: ApprovalPause}); reason != "" {
		t.Fatalf("pause should not deny: %q", reason)
	}
	if reason := ToolApprovalDenialReason(Tool{Approval: ApprovalAlways}); reason != "tool requires approval" {
		t.Fatalf("always without gate path: %q", reason)
	}
	if reason := ToolApprovalDenialReason(Tool{Approval: ApprovalRisky, SideEffect: SideEffectRead}); reason != "" {
		t.Fatalf("risky read should not deny: %q", reason)
	}
	if reason := ToolApprovalDenialReason(Tool{Approval: ApprovalRisky, SideEffect: SideEffectWrite}); reason != "risky tool requires approval" {
		t.Fatalf("risky write: %q", reason)
	}
}

func TestToolApprovalPauseRequired(t *testing.T) {
	cases := []struct {
		tool Tool
		want bool
	}{
		{Tool{Approval: ApprovalNever}, false},
		{Tool{Approval: ApprovalPause}, true},
		{Tool{Approval: ApprovalAlways}, true},
		{Tool{Approval: ApprovalRisky, SideEffect: SideEffectRead}, false},
		{Tool{Approval: ApprovalRisky, SideEffect: SideEffectDangerous}, true},
	}
	for _, tc := range cases {
		if got := ToolApprovalPauseRequired(tc.tool); got != tc.want {
			t.Fatalf("tool %+v pause=%v want %v", tc.tool, got, tc.want)
		}
	}
}

func TestHasHumanCheckpoint(t *testing.T) {
	if !HasHumanCheckpoint([]string{"before_final_answer"}, CheckpointBeforeFinalAnswer) {
		t.Fatal("expected match")
	}
	if HasHumanCheckpoint(nil, CheckpointBeforeFinalAnswer) {
		t.Fatal("expected no match")
	}
}
