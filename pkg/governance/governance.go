package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

var ErrDenied = errors.New("governance: denied")

type ToolInvocation struct {
	RunID      string
	Agent      string
	Tool       string
	SideEffect core.SideEffectLevel
	Input      json.RawMessage
	CallCount  int
	TotalCalls int
	Metadata   map[string]string
}

type ToolPolicy interface {
	AuthorizeTool(ctx context.Context, invocation ToolInvocation) error
}

type ToolPolicyFunc func(ctx context.Context, invocation ToolInvocation) error

func (fn ToolPolicyFunc) AuthorizeTool(ctx context.Context, invocation ToolInvocation) error {
	return fn(ctx, invocation)
}

type OutputRedaction struct {
	RunID  string
	StepID string
	Kind   string
	Data   json.RawMessage
}

type OutputRedactor interface {
	RedactOutput(ctx context.Context, output OutputRedaction) (json.RawMessage, error)
}

type OutputRedactorFunc func(ctx context.Context, output OutputRedaction) (json.RawMessage, error)

func (fn OutputRedactorFunc) RedactOutput(ctx context.Context, output OutputRedaction) (json.RawMessage, error) {
	return fn(ctx, output)
}

func NewMaxSideEffectPolicy(max core.SideEffectLevel) ToolPolicy {
	return ToolPolicyFunc(func(ctx context.Context, invocation ToolInvocation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sideEffectRank(invocation.SideEffect) > sideEffectRank(max) {
			return fmt.Errorf("%w: tool %q side effect %q exceeds allowed %q", ErrDenied, invocation.Tool, invocation.SideEffect, max)
		}
		return nil
	})
}

func NewToolBudgetPolicy(maxCalls int) ToolPolicy {
	return ToolPolicyFunc(func(ctx context.Context, invocation ToolInvocation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if maxCalls > 0 && invocation.TotalCalls >= maxCalls {
			return fmt.Errorf("%w: run tool budget exceeded: %d call(s)", ErrDenied, maxCalls)
		}
		return nil
	})
}

func ChainToolPolicies(policies ...ToolPolicy) ToolPolicy {
	return ToolPolicyFunc(func(ctx context.Context, invocation ToolInvocation) error {
		for _, policy := range policies {
			if policy == nil {
				continue
			}
			if err := policy.AuthorizeTool(ctx, invocation); err != nil {
				return err
			}
		}
		return nil
	})
}

func NewJSONFieldRedactor(fields ...string) OutputRedactor {
	redacted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(strings.ToLower(field))
		if field != "" {
			redacted[field] = struct{}{}
		}
	}
	return OutputRedactorFunc(func(ctx context.Context, output OutputRedaction) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(output.Data) == 0 || len(redacted) == 0 {
			return cloneRaw(output.Data), nil
		}
		var value any
		if err := json.Unmarshal(output.Data, &value); err != nil {
			return nil, fmt.Errorf("governance: decode output for redaction: %w", err)
		}
		value = redactValue(value, redacted)
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("governance: encode redacted output: %w", err)
		}
		return data, nil
	})
}

func sideEffectRank(level core.SideEffectLevel) int {
	switch level {
	case "", core.SideEffectNone:
		return 0
	case core.SideEffectRead:
		return 1
	case core.SideEffectWrite:
		return 2
	case core.SideEffectExternal:
		return 3
	case core.SideEffectDangerous:
		return 4
	default:
		return 4
	}
}

func redactValue(value any, fields map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := fields[strings.ToLower(key)]; ok {
				typed[key] = "[REDACTED]"
				continue
			}
			typed[key] = redactValue(child, fields)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = redactValue(child, fields)
		}
		return typed
	default:
		return typed
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}
