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

func TestChainToolPoliciesRunsInOrder(t *testing.T) {
	calls := 0
	chain := ChainToolPolicies(
		ToolPolicyFunc(func(context.Context, ToolInvocation) error {
			calls++
			return nil
		}),
		NewToolBudgetPolicy(1),
	)
	if err := chain.AuthorizeTool(context.Background(), ToolInvocation{TotalCalls: 0}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected first policy to run, calls=%d", calls)
	}
	if err := chain.AuthorizeTool(context.Background(), ToolInvocation{TotalCalls: 1}); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected budget denial, got %v", err)
	}
}

func TestMaxSideEffectPolicyRanksUnknownAsDangerous(t *testing.T) {
	policy := NewMaxSideEffectPolicy(core.SideEffectRead)
	err := policy.AuthorizeTool(context.Background(), ToolInvocation{Tool: "mystery", SideEffect: core.SideEffectLevel("unknown")})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial for unknown side effect, got %v", err)
	}
}

func TestJSONFieldRedactorReturnsCloneForEmptyInput(t *testing.T) {
	raw := json.RawMessage(`{"ok":true}`)
	out, err := NewJSONFieldRedactor().RedactOutput(context.Background(), OutputRedaction{Data: raw})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(raw) {
		t.Fatalf("expected clone, got %s", out)
	}
}
