package feature

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
)

type namedFeature struct{ name string }

func (f namedFeature) Name() string { return f.name }

type wrapFeature struct {
	namedFeature
	order *[]string
	mark  string
}

func (f wrapFeature) WrapLLMGateway(inner llm.ToolCaller) llm.ToolCaller {
	mark := f.mark
	order := f.order
	return toolCallerFunc(func(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
		*order = append(*order, mark)
		return inner.ChatWithTools(ctx, profile, req)
	})
}

type toolCallerFunc func(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error)

func (fn toolCallerFunc) ChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return fn(ctx, profile, req)
}

type inspectorFeature struct {
	namedFeature
	inspectors []toolinspect.Inspector
}

func (f inspectorFeature) ToolInspectors() []toolinspect.Inspector { return f.inspectors }

type hookFeature struct {
	namedFeature
	hooks LoopHooks
}

func (f hookFeature) LoopHooks() LoopHooks { return f.hooks }

type stopFeature struct {
	namedFeature
	conditions []StopCondition
}

func (f stopFeature) StopConditions() []StopCondition { return f.conditions }

// panicInspectorFeature panics while contributing inspectors; its loop hooks
// must still be collected (per-contribution isolation).
type panicInspectorFeature struct {
	namedFeature
	hooks LoopHooks
}

func (f panicInspectorFeature) ToolInspectors() []toolinspect.Inspector { panic("broken feature") }
func (f panicInspectorFeature) LoopHooks() LoopHooks                    { return f.hooks }

type stubInspector struct{ name string }

func (s stubInspector) Name() string { return s.name }
func (s stubInspector) Inspect(context.Context, *toolinspect.Request) (toolinspect.Finding, error) {
	return toolinspect.AllowFinding, nil
}

func TestCollectGathersEveryContributionKind(t *testing.T) {
	var order []string
	hook := LoopHooks{OnStepFinish: func(context.Context, StepInfo) {}}
	stop := StopCondition(func(context.Context, StepInfo) (string, bool) { return "", false })
	contributions := Collect([]Feature{
		wrapFeature{namedFeature: namedFeature{"wrap"}, order: &order, mark: "w1"},
		inspectorFeature{namedFeature: namedFeature{"insp"}, inspectors: []toolinspect.Inspector{stubInspector{"a"}, nil}},
		hookFeature{namedFeature: namedFeature{"hook"}, hooks: hook},
		stopFeature{namedFeature: namedFeature{"stop"}, conditions: []StopCondition{stop, nil}},
		nil, // nil features are skipped
	}, nil)
	if len(contributions.LLMWrappers) != 1 {
		t.Fatalf("LLMWrappers=%d want 1", len(contributions.LLMWrappers))
	}
	if len(contributions.Inspectors) != 1 {
		t.Fatalf("Inspectors=%d want 1 (nil entries dropped)", len(contributions.Inspectors))
	}
	if len(contributions.Hooks) != 1 || contributions.Hooks[0].OnStepFinish == nil {
		t.Fatalf("Hooks=%+v want one hook", contributions.Hooks)
	}
	if len(contributions.StopConditions) != 1 {
		t.Fatalf("StopConditions=%d want 1 (nil entries dropped)", len(contributions.StopConditions))
	}
	if contributions.Empty() {
		t.Fatal("contributions must not be empty")
	}
}

func TestCollectIsolatesPanickingFeature(t *testing.T) {
	hook := LoopHooks{OnStepFinish: func(context.Context, StepInfo) {}}
	contributions := Collect([]Feature{
		panicInspectorFeature{namedFeature: namedFeature{"panic"}, hooks: hook},
		inspectorFeature{namedFeature: namedFeature{"healthy"}, inspectors: []toolinspect.Inspector{stubInspector{"ok"}}},
	}, nil)
	if len(contributions.Inspectors) != 1 || contributions.Inspectors[0].Name() != "ok" {
		t.Fatalf("panicking feature must lose only its own inspectors, got %+v", contributions.Inspectors)
	}
	if len(contributions.Hooks) != 1 {
		t.Fatalf("panicking feature's other contributions must survive, hooks=%d", len(contributions.Hooks))
	}
}

func TestCollectWrapperOrderIsFeatureOrder(t *testing.T) {
	var order []string
	contributions := Collect([]Feature{
		wrapFeature{namedFeature: namedFeature{"first"}, order: &order, mark: "first"},
		wrapFeature{namedFeature: namedFeature{"second"}, order: &order, mark: "second"},
	}, nil)
	// Apply like the runtime: first feature wraps innermost.
	var caller llm.ToolCaller = toolCallerFunc(func(context.Context, string, llm.ToolCallRequest) (llm.ToolCallResponse, error) {
		return llm.ToolCallResponse{}, nil
	})
	for _, wrap := range contributions.LLMWrappers {
		caller = wrap(caller)
	}
	if _, err := caller.ChatWithTools(context.Background(), "p", llm.ToolCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Fatalf("wrap invocation order=%v want [second first] (first feature innermost)", order)
	}
}

func TestUsageAccountingAccumulates(t *testing.T) {
	var reports []llm.TokenUsage
	accounting := NewUsageAccounting(func(_ context.Context, total llm.TokenUsage) {
		reports = append(reports, total)
	})
	hooks := accounting.LoopHooks()
	steps := []StepInfo{
		{RunID: "r", Step: 1, ToolCalls: 1, Usage: llm.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		{RunID: "r", Step: 2, Usage: llm.TokenUsage{InputTokens: 20, OutputTokens: 7, ReasoningTokens: 3, TotalTokens: 30}},
	}
	for _, info := range steps {
		hooks.OnStepFinish(context.Background(), info)
	}
	total := accounting.Total()
	if total.InputTokens != 30 || total.OutputTokens != 12 || total.ReasoningTokens != 3 || total.TotalTokens != 45 {
		t.Fatalf("Total()=%+v want {30 12 3 45}", total)
	}
	if accounting.Steps() != 2 {
		t.Fatalf("Steps()=%d want 2", accounting.Steps())
	}
	if len(reports) != 2 || reports[1].TotalTokens != 45 {
		t.Fatalf("reports=%+v want running totals after each step", reports)
	}
	if accounting.Name() != "usage_accounting" {
		t.Fatalf("Name()=%q", accounting.Name())
	}
}
