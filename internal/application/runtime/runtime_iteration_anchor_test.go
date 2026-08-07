package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// F4 (DEFECT_REPORT second round): the delta baseline must be content-anchored,
// not length-only. A context compaction that shrinks the conversation below the
// recorded baseline followed by re-growth past it makes base <= len(messages)
// again, but messages[:base] is no longer the persisted prefix — a delta would
// fold back into a duplicated/garbled conversation. The writer must detect the
// anchor mismatch and degrade to a full snapshot.
func TestEngineAutonomousIterationAnchorMismatchFallsBackToFull(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 8), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-anchor", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	convo := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "r1"},
	}
	if err := engine.persistAutonomousIteration(ctx, "run-anchor", 1, convo, newToolCallTracker(), 0); err != nil {
		t.Fatal(err)
	}
	// Compaction rewrote the prefix (different content, same-or-smaller
	// length), then the conversation grew past the recorded baseline again.
	regrown := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a1 (compacted summary)"},
		{Role: llm.RoleTool, ToolCallID: "call-9", Content: "r9"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleTool, ToolCallID: "call-2", Content: "r2"},
	}
	if err := engine.persistAutonomousIteration(ctx, "run-anchor", 2, regrown, newToolCallTracker(), 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(ctx, "run-anchor")
	if err != nil {
		t.Fatal(err)
	}
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:2")
	if envelope.Format != iterationEnvelopeFormatFull {
		t.Fatalf("anchor-mismatched boundary must degrade to a full snapshot, got format=%q base=%d", envelope.Format, envelope.Base)
	}
	rebuilt, _, err := engine.loadAutonomousConversation(ctx, "run-anchor", snapshot.StepOutputs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt) != len(regrown) {
		t.Fatalf("rebuilt %d messages, want %d (the regrown conversation)", len(rebuilt), len(regrown))
	}
	for i := range regrown {
		if !messagesEqual(regrown[i], rebuilt[i]) {
			t.Fatalf("message %d diverges:\nregrown: %+v\nrebuilt: %+v", i, regrown[i], rebuilt[i])
		}
	}
}

// F4: with an intact prefix the delta keeps its content anchor, and the fold
// side verifies it — a tampered anchor fails the rebuild loudly instead of
// producing a corrupted conversation.
func TestEngineAutonomousIterationDeltaCarriesAndVerifiesAnchor(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 8), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-anchor-ok", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	convo := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}
	if err := engine.persistAutonomousIteration(ctx, "run-anchor-ok", 1, convo, newToolCallTracker(), 0); err != nil {
		t.Fatal(err)
	}
	grown := append(append([]llm.Message(nil), convo...), llm.Message{Role: llm.RoleAssistant, Content: "a2"})
	if err := engine.persistAutonomousIteration(ctx, "run-anchor-ok", 2, grown, newToolCallTracker(), 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(ctx, "run-anchor-ok")
	if err != nil {
		t.Fatal(err)
	}
	envelope := iterationEnvelopeAt(t, ctx, nil, snapshot, "auto:iter:2")
	if envelope.Format != iterationEnvelopeFormatDelta {
		t.Fatalf("intact prefix must stay a delta, got %q", envelope.Format)
	}
	if envelope.BaseHash == "" {
		t.Fatal("delta envelope must carry the content anchor of its base prefix")
	}
	if envelope.BaseHash != iterationMessageHash(convo[len(convo)-1]) {
		t.Fatal("delta anchor must hash the last message of the persisted prefix")
	}
	if _, _, err := engine.loadAutonomousConversation(ctx, "run-anchor-ok", snapshot.StepOutputs, 2); err != nil {
		t.Fatalf("intact chain must fold: %v", err)
	}
	// A delta whose anchor does not match the rebuilt prefix is corruption:
	// the fold must fail, not silently append onto the wrong base.
	raw, err := json.Marshal(iterationEnvelope{
		Format:   iterationEnvelopeFormatDelta,
		Base:     len(convo),
		BaseHash: "sha256:deadbeef",
		Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "rogue"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.StepOutputs["auto:iter:3"] = runstate.StepOutputRef{Inline: raw}
	if _, _, err := engine.loadAutonomousConversation(ctx, "run-anchor-ok", snapshot.StepOutputs, 3); err == nil ||
		!strings.Contains(err.Error(), "anchor") {
		t.Fatalf("anchor-mismatched delta must fail the fold, got %v", err)
	}
}

// F5 (DEFECT_REPORT second round): a tool-approval pause returns before the
// iteration boundary persist, so the paused step's number is a hole in the
// auto:iter chain (1..N-1, then N+1...). Crash recovery must fold across that
// hole instead of failing the run permanently with "checkpoint chain has a
// gap": a full envelope after the hole re-anchors the chain, and a delta
// spanning the hole is validated by its base (and anchor) rather than by key
// contiguity.
func TestEngineLoadAutonomousConversationFoldsAcrossPauseHole(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 8), Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(env iterationEnvelope) runstate.StepOutputRef {
		raw, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		return runstate.StepOutputRef{Inline: raw}
	}
	iter1 := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "r1"},
	}
	// Cross-process shape: the post-resume boundary is a full snapshot (the
	// in-memory baseline was lost with the worker), the pause step left no
	// auto:iter:2.
	t.Run("full re-anchor after the hole", func(t *testing.T) {
		resumed := append(append([]llm.Message(nil), iter1...),
			llm.Message{Role: llm.RoleAssistant, Content: "a2"},
			llm.Message{Role: llm.RoleTool, ToolCallID: "call-2", Content: "r2"},
		)
		outputs := map[string]runstate.StepOutputRef{
			"auto:iter:1": mk(iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: iter1}),
			"auto:iter:3": mk(iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: resumed}),
		}
		messages, _, err := engine.loadAutonomousConversation(ctx, "run-hole", outputs, 3)
		if err != nil {
			t.Fatalf("fold must cross the pause hole via the full anchor, got %v", err)
		}
		if len(messages) != len(resumed) {
			t.Fatalf("rebuilt %d messages, want %d", len(messages), len(resumed))
		}
	})
	// In-process shape: the baseline survived, so the post-resume boundary is
	// a delta whose base predates the hole (the paused step's messages ride
	// the delta itself).
	t.Run("delta spanning the hole", func(t *testing.T) {
		spanning := []llm.Message{
			{Role: llm.RoleAssistant, Content: "a2"},
			{Role: llm.RoleTool, ToolCallID: "call-2", Content: "r2"},
		}
		outputs := map[string]runstate.StepOutputRef{
			"auto:iter:1": mk(iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: iter1}),
			"auto:iter:3": mk(iterationEnvelope{
				Format:   iterationEnvelopeFormatDelta,
				Base:     len(iter1),
				BaseHash: iterationMessageHash(iter1[len(iter1)-1]),
				Messages: spanning,
			}),
		}
		messages, _, err := engine.loadAutonomousConversation(ctx, "run-hole", outputs, 3)
		if err != nil {
			t.Fatalf("fold must accept a delta spanning the pause hole, got %v", err)
		}
		if len(messages) != len(iter1)+len(spanning) {
			t.Fatalf("rebuilt %d messages, want %d", len(messages), len(iter1)+len(spanning))
		}
	})
	// A hole the following delta does NOT span (base points past the rebuilt
	// prefix) is genuine corruption and must still fail.
	t.Run("unspanned hole still fails", func(t *testing.T) {
		outputs := map[string]runstate.StepOutputRef{
			"auto:iter:1": mk(iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: iter1}),
			"auto:iter:3": mk(iterationEnvelope{
				Format:   iterationEnvelopeFormatDelta,
				Base:     len(iter1) + 2,
				BaseHash: iterationMessageHash(iter1[len(iter1)-1]),
				Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "a3"}},
			}),
		}
		if _, _, err := engine.loadAutonomousConversation(ctx, "run-hole", outputs, 3); err == nil {
			t.Fatal("a hole the next delta does not span must fail the fold")
		}
	})
}

// F7 (DEFECT_REPORT second round): importing the checkpointed approval cache
// is regenerable, cache-like state — a corrupt payload must degrade to a warn
// plus an empty cache (fail-open, matching the "store without RunStateExporter"
// degradation), not a permanent run failure.
type failingImportApprovalStore struct {
	toolorch.ApprovalStore
	err error
}

func (s failingImportApprovalStore) ExportRun(string) (json.RawMessage, bool) { return nil, false }
func (s failingImportApprovalStore) ImportRun(string, json.RawMessage) error  { return s.err }

type warnCapture struct{ warns []string }

func (l *warnCapture) Warn(_ context.Context, msg string, _ ...any) { l.warns = append(l.warns, msg) }
func (l *warnCapture) Error(context.Context, string, ...any)        {}

func TestEngineRestoreApprovalCheckpointStateFailsOpen(t *testing.T) {
	logger := &warnCapture{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:          runstateinmem.NewRepository(),
		Logger:        logger,
		ApprovalStore: failingImportApprovalStore{ApprovalStore: toolorch.NewMemoryApprovalStore(), err: errors.New("corrupt approvals payload")},
	})
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]json.RawMessage{
		checkpointApprovalsVar: json.RawMessage(`{"risky":"allow"}`),
		checkpointDenyCountVar: json.RawMessage(`2`),
	}
	// Must not panic or fail the run: the import error degrades to a warn.
	engine.restoreApprovalCheckpointState(context.Background(), "run-f7", vars)
	if len(logger.warns) == 0 {
		t.Fatal("the failed import must be logged at warn level")
	}
}
