package toolinvoke_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestAutoRetrySafe(t *testing.T) {
	if !toolinvoke.AutoRetrySafe(core.Tool{SideEffect: core.SideEffectRead}) {
		t.Fatal("read tools should auto-retry")
	}
	if toolinvoke.AutoRetrySafe(core.Tool{SideEffect: core.SideEffectWrite}) {
		t.Fatal("write tools must not auto-retry")
	}
}

func TestValidateInput(t *testing.T) {
	tool := core.Tool{
		InputSchema: json.RawMessage(`{"type":"object","required":["q"],"properties":{"q":{"type":"string"}}}`),
	}
	if err := toolinvoke.ValidateInput(true, tool, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected validation error")
	}
	if err := toolinvoke.ValidateInput(false, tool, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("disabled validation must pass: %v", err)
	}
}

func TestDenialWithoutGate(t *testing.T) {
	always := core.Tool{Approval: core.ApprovalAlways}
	if got := toolinvoke.DenialWithoutGate(always, false, false); !strings.Contains(got, "approval") {
		t.Fatalf("always without gate should deny, got %q", got)
	}
	if got := toolinvoke.DenialWithoutGate(always, true, false); got != "" {
		t.Fatalf("always with gate should defer to pause path, got %q", got)
	}
	pause := core.Tool{Approval: core.ApprovalPause}
	if got := toolinvoke.DenialWithoutGate(pause, false, false); got == "" {
		t.Fatal("pause without gate should deny")
	}
}
