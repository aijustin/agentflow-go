package governance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestMaxSideEffectPolicyDeniesAboveThreshold(t *testing.T) {
	policy := NewMaxSideEffectPolicy(core.SideEffectRead)
	if err := policy.AuthorizeTool(context.Background(), ToolInvocation{Tool: "read", SideEffect: core.SideEffectRead}); err != nil {
		t.Fatal(err)
	}
	err := policy.AuthorizeTool(context.Background(), ToolInvocation{Tool: "write", SideEffect: core.SideEffectWrite})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denied error, got %v", err)
	}
}

func TestToolBudgetPolicyDeniesAfterBudget(t *testing.T) {
	policy := NewToolBudgetPolicy(1)
	if err := policy.AuthorizeTool(context.Background(), ToolInvocation{Tool: "first", TotalCalls: 0}); err != nil {
		t.Fatal(err)
	}
	err := policy.AuthorizeTool(context.Background(), ToolInvocation{Tool: "second", TotalCalls: 1})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denied error, got %v", err)
	}
}

func TestJSONFieldRedactorRedactsNestedFields(t *testing.T) {
	redactor := NewJSONFieldRedactor("secret", "token")
	out, err := redactor.RedactOutput(context.Background(), OutputRedaction{Data: json.RawMessage(`{"user":"ok","secret":"s1","nested":{"token":"t1"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"nested":{"token":"[REDACTED]"},"secret":"[REDACTED]","user":"ok"}` {
		t.Fatalf("unexpected redacted output: %s", out)
	}
}
