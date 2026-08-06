package agentflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/feature"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
)

// featureScenario is a one-tool autonomous agent used by the feature/inspector
// wiring tests.
func featureScenario() core.Scenario {
	return core.Scenario{
		Name: "features",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever, InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
}

func queueEchoThenAnswerGW(gateway *llmmock.Gateway) {
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}},
		ChatResponse: llm.ChatResponse{Usage: llm.TokenUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:   llm.TokenUsage{InputTokens: 20, OutputTokens: 6, TotalTokens: 26},
		},
	})
}

// toolDeniedPayload decodes the payloads of all ToolDenied events.
func toolDeniedPayload(sink *terminalEventSink) []map[string]any {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []map[string]any
	for _, event := range sink.events {
		if event.Type != core.EventToolDenied {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			out = append(out, payload)
		}
	}
	return out
}

// WithToolInspectors wires a host denial around the built-in gates.
func TestFrameworkWithToolInspectorsHostDeny(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswerGW(gateway)
	sink := &terminalEventSink{}
	fw, err := agentflow.New(
		featureScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithEventSink(sink),
		agentflow.WithToolInspectors([]toolinspect.Inspector{
			toolinspect.InspectorFunc{
				InspectorName: "host_policy",
				Fn: func(_ context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
					return toolinspect.Deny("host_policy", "blocked by host"), nil
				},
			},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-insp", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("output=%q want done", result.Output)
	}
	denials := toolDeniedPayload(sink)
	if len(denials) != 1 || denials[0]["kind"] != "host_policy" {
		t.Fatalf("unexpected denials: %v", denials)
	}
}

type middlewareFeature struct {
	calls *int
}

func (middlewareFeature) Name() string { return "middleware" }

func (f middlewareFeature) WrapLLMGateway(inner llm.ToolCaller) llm.ToolCaller {
	return toolCallerWrap{inner: inner, calls: f.calls}
}

type toolCallerWrap struct {
	inner llm.ToolCaller
	calls *int
}

func (w toolCallerWrap) ChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	*w.calls++
	return w.inner.ChatWithTools(ctx, profile, req)
}

type hooksFeature struct {
	mu    *sync.Mutex
	steps *[]feature.StepInfo
}

func (hooksFeature) Name() string { return "hooks" }

func (f hooksFeature) LoopHooks() feature.LoopHooks {
	return feature.LoopHooks{OnStepFinish: func(_ context.Context, info feature.StepInfo) {
		f.mu.Lock()
		defer f.mu.Unlock()
		*f.steps = append(*f.steps, info)
	}}
}

type stopFeature struct {
	afterStep int
	reason    string
}

func (stopFeature) Name() string { return "stop" }

func (f stopFeature) StopConditions() []feature.StopCondition {
	return []feature.StopCondition{
		func(_ context.Context, info feature.StepInfo) (string, bool) {
			if info.Step >= f.afterStep {
				return f.reason, true
			}
			return "", false
		},
	}
}

// WithFeatures wires LLM middleware and loop hooks; the usage-accounting
// built-in validates the extension point end to end.
func TestFrameworkWithFeaturesMiddlewareHooksUsage(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswerGW(gateway)
	var middlewareCalls int
	var mu sync.Mutex
	var steps []feature.StepInfo
	accounting := feature.NewUsageAccounting(nil)
	fw, err := agentflow.New(
		featureScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithFeatures(
			middlewareFeature{calls: &middlewareCalls},
			hooksFeature{mu: &mu, steps: &steps},
			accounting,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-features", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("output=%q want done", result.Output)
	}
	if middlewareCalls != 2 {
		t.Fatalf("middleware saw %d LLM calls, want 2", middlewareCalls)
	}
	if len(steps) != 2 {
		t.Fatalf("loop hook fired %d times, want 2 (tool step + final step)", len(steps))
	}
	if steps[0].ToolCalls != 1 || steps[0].Step != 1 {
		t.Fatalf("first step info=%+v", steps[0])
	}
	if steps[1].ToolCalls != 0 {
		t.Fatalf("final step info=%+v", steps[1])
	}
	total := accounting.Total()
	if total.InputTokens != 30 || total.OutputTokens != 10 || total.TotalTokens != 40 {
		t.Fatalf("usage accounting total=%+v want {30 10 40}", total)
	}
}

// A feature stop condition halts the run and its reason becomes the terminal
// payload's termination_reason.
func TestFrameworkWithFeaturesStopConditionTerminatesRun(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswerGW(gateway)
	sink := &terminalEventSink{}
	fw, err := agentflow.New(
		featureScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithEventSink(sink),
		agentflow.WithFeatures(stopFeature{afterStep: 1, reason: "budget_exceeded"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-stop", Agent: "assistant", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "budget_exceeded") {
		t.Fatalf("expected stop-condition error, got %v", err)
	}
	payload := sink.terminalPayload(t, core.EventRunFailed)
	if payload.TerminationReason != "budget_exceeded" {
		t.Fatalf("TerminationReason=%q want budget_exceeded", payload.TerminationReason)
	}
}

// A panicking feature contribution is dropped without affecting the run.
func TestFrameworkWithFeaturesPanicIsolation(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswerGW(gateway)
	fw, err := agentflow.New(
		featureScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithFeatures(panicInspectorFW{}, hooksFeatureOK{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-panic", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" {
		t.Fatalf("output=%q want done", result.Output)
	}
	if !hooksFeatureOKFired {
		t.Fatal("healthy feature's hook must still fire despite the panicking feature")
	}
}

type panicInspectorFW struct{}

func (panicInspectorFW) Name() string { return "panic" }

func (panicInspectorFW) ToolInspectors() []toolinspect.Inspector { panic("broken") }

var hooksFeatureOKFired bool

type hooksFeatureOK struct{}

func (hooksFeatureOK) Name() string { return "healthy" }

func (hooksFeatureOK) LoopHooks() feature.LoopHooks {
	return feature.LoopHooks{OnStepFinish: func(context.Context, feature.StepInfo) {
		hooksFeatureOKFired = true
	}}
}
