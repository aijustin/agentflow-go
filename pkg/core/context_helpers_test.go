package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestFrameworkBuildInfoHelpers(t *testing.T) {
	_ = core.FrameworkVersion()
	_ = core.FrameworkCommit()
	fields := core.FrameworkBuildFields()
	if fields == nil {
		t.Fatal("expected non-nil build fields map")
	}
}

func TestEpisodeCorrelationContext(t *testing.T) {
	empty := core.EpisodeCorrelation{}
	if !empty.Empty() {
		t.Fatal("zero correlation should be empty")
	}
	ctx := core.ContextWithEpisodeCorrelation(context.Background(), empty)
	if got := core.EpisodeCorrelationFromContext(ctx); !got.Empty() {
		t.Fatalf("empty correlation must not attach: %+v", got)
	}
	corr := core.EpisodeCorrelation{EpisodeID: "ep", TriggerKind: core.TriggerKindUser, SessionID: "sess"}
	if corr.Empty() {
		t.Fatal("populated correlation should not be empty")
	}
	ctx = core.ContextWithEpisodeCorrelation(context.Background(), corr)
	got := core.EpisodeCorrelationFromContext(ctx)
	if got != corr {
		t.Fatalf("got %+v want %+v", got, corr)
	}
}

func TestTrustModeContext(t *testing.T) {
	ctx := core.ContextWithTrustMode(context.Background(), "")
	if got := core.TrustModeFromContext(ctx); got != "" {
		t.Fatalf("empty mode must not attach, got %q", got)
	}
	ctx = core.ContextWithTrustMode(context.Background(), core.TrustModeFullTrust)
	if got := core.TrustModeFromContext(ctx); got != core.TrustModeFullTrust {
		t.Fatalf("got %q", got)
	}
}

func TestToolResolverFunc(t *testing.T) {
	called := false
	resolver := core.ToolResolverFunc(func(ctx context.Context, tool core.Tool) (core.ToolExecutor, error) {
		called = true
		return nil, nil
	})
	if _, err := resolver.ResolveTool(context.Background(), core.Tool{Name: "echo"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected resolver func to run")
	}
}

func TestBuildLifecyclePayloadTerminalBranches(t *testing.T) {
	corr := core.EpisodeCorrelation{EpisodeID: "ep", TriggerKind: "user", SessionID: "s"}
	failed := core.BuildLifecyclePayload(core.EventRunFailed, json.RawMessage(`{"error":"boom"}`), corr)
	var failedPayload core.RunTerminalPayload
	if err := json.Unmarshal(failed, &failedPayload); err != nil {
		t.Fatal(err)
	}
	if failedPayload.Status != "failed" || failedPayload.Error != "boom" {
		t.Fatalf("unexpected failed payload: %+v", failedPayload)
	}

	cancelled := core.BuildLifecyclePayload(core.EventRunCancelled, nil, corr)
	var cancelledPayload core.RunTerminalPayload
	if err := json.Unmarshal(cancelled, &cancelledPayload); err != nil {
		t.Fatal(err)
	}
	if cancelledPayload.Status != "cancelled" {
		t.Fatalf("unexpected cancelled payload: %+v", cancelledPayload)
	}

	// Non-object payload is wrapped under "data" when merging correlation.
	started := core.BuildLifecyclePayload(core.EventRunStarted, json.RawMessage(`"plain"`), corr)
	var startedFields map[string]json.RawMessage
	if err := json.Unmarshal(started, &startedFields); err != nil {
		t.Fatal(err)
	}
	if string(startedFields["data"]) != `"plain"` {
		t.Fatalf("expected data wrapper, got %s", started)
	}

	// String error payload and raw fallback for extractLifecycleError.
	failedStr := core.BuildLifecyclePayload(core.EventRunFailed, json.RawMessage(`"string-err"`), corr)
	var failedStrPayload core.RunTerminalPayload
	if err := json.Unmarshal(failedStr, &failedStrPayload); err != nil {
		t.Fatal(err)
	}
	if failedStrPayload.Error != "string-err" {
		t.Fatalf("expected string error, got %q", failedStrPayload.Error)
	}
	failedRaw := core.BuildLifecyclePayload(core.EventRunFailed, json.RawMessage(`[1]`), corr)
	var failedRawPayload core.RunTerminalPayload
	if err := json.Unmarshal(failedRaw, &failedRawPayload); err != nil {
		t.Fatal(err)
	}
	if failedRawPayload.Error != "[1]" {
		t.Fatalf("expected raw error fallback, got %q", failedRawPayload.Error)
	}

	completed := core.BuildRunCompletedPayload(json.RawMessage(`"done"`), corr, &core.RunUsage{InputTokens: 1})
	var completedPayload core.RunTerminalPayload
	if err := json.Unmarshal(completed, &completedPayload); err != nil {
		t.Fatal(err)
	}
	if completedPayload.Status != "completed" || completedPayload.Usage == nil || completedPayload.Usage.InputTokens != 1 {
		t.Fatalf("unexpected completed payload: %+v", completedPayload)
	}

	paused := core.BuildPausedOutcomePayload(corr)
	var pausedPayload core.RunTerminalPayload
	if err := json.Unmarshal(paused, &pausedPayload); err != nil {
		t.Fatal(err)
	}
	if pausedPayload.Status != "paused" || pausedPayload.OutcomeKind != "paused" {
		t.Fatalf("unexpected paused payload: %+v", pausedPayload)
	}

	// Non-lifecycle types pass through unchanged.
	passthrough := json.RawMessage(`{"keep":true}`)
	if got := string(core.BuildLifecyclePayload(core.EventToolCalled, passthrough, corr)); got != string(passthrough) {
		t.Fatalf("non-lifecycle payload mutated: %s", got)
	}
}

func TestDisplayLabelAndEventCategory(t *testing.T) {
	labels := map[core.EventType]string{
		core.EventRunStarted:                "Run started",
		core.EventRunCompleted:              "Run completed",
		core.EventRunFailed:                 "Run failed",
		core.EventRunCancelled:              "Run cancelled",
		core.EventRunPaused:                 "Run paused",
		core.EventRunResumed:                "Run resumed",
		core.EventStepStarted:               "Step started",
		core.EventStepCompleted:             "Step completed",
		core.EventStepFailed:                "Step failed",
		core.EventSubgraphStarted:           "Subgraph started",
		core.EventSubgraphCompleted:         "Subgraph completed",
		core.EventToolCalled:                "Tool called",
		core.EventToolReturned:              "Tool returned",
		core.EventToolDenied:                "Tool denied",
		core.EventLLMCalled:                 "LLM called",
		core.EventLLMReturned:               "LLM returned",
		core.EventLLMTokenUsage:             "LLM token usage",
		core.EventHumanGateOpened:           "Human gate opened",
		core.EventHumanGateDecided:          "Human gate decided",
		core.EventHumanGateExpired:          "Human gate expired",
		core.EventMemoryRead:                "Memory read",
		core.EventMemoryWrite:               "Memory write",
		core.EventMemoryPromoted:            "Memory promoted",
		core.EventMemoryDemoted:             "Memory demoted",
		core.EventMemoryEvicted:             "Memory evicted",
		core.EventContextPrepared:           "Context prepared",
		core.EventContextIncomplete:         "Context incomplete",
		core.EventSkillApplied:              "Skill applied",
		core.EventCompletionRecovery:        "Completion recovery",
		core.EventCompletionRequirementFail: "Completion requirement failed",
		core.EventInterjectionDrained:       "Interjection drained",
		core.EventHITLDenyBreakerTripped:    "HITL deny breaker tripped",
		core.EventTurnStopContinued:         "Turn stop continued",
		core.EventType("custom.event"):      "custom.event",
	}
	for typ, want := range labels {
		if got := core.DisplayLabel(typ); got != want {
			t.Fatalf("DisplayLabel(%s)=%q want %q", typ, got, want)
		}
	}
	if got := core.EventCategory(core.EventToolCalled); got != "tool" {
		t.Fatalf("tool category=%q", got)
	}
	if got := core.EventCategory(core.EventSkillApplied); got != "skill" {
		t.Fatalf("skill category=%q", got)
	}
	if got := core.EventCategory(core.EventLLMCalled); got != "llm" {
		t.Fatalf("llm category=%q", got)
	}
	if got := core.EventCategory(core.EventMemoryWrite); got != "memory" {
		t.Fatalf("memory category=%q", got)
	}
	if got := core.EventCategory(core.EventContextPrepared); got != "run" {
		t.Fatalf("context category=%q", got)
	}
	if got := core.EventCategory(core.EventInterjectionDrained); got != "run" {
		t.Fatalf("interjection category=%q", got)
	}
}
