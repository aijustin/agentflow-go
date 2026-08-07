package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// AutonomousIterationStepPrefix prefixes the StepOutputs keys the autonomous
// tool loop writes at every iteration boundary. The value under
// "auto:iter:<n>" is an iterationEnvelope: the conversation DELTA since
// iteration n-1 (plus the baseline message count it builds on), so iteration
// n costs O(new messages) instead of rewriting the whole transcript.
// ResumeAutonomousFromIteration rebuilds the full conversation by folding the
// envelopes in order, and RetryFailedRun resumes a crashed autonomous run
// from the highest persisted iteration instead of requiring a HITL gate
// checkpoint.
//
// BREAKING (2026-08): the format changed from a bare []llm.Message (the full
// conversation) to the envelope. Snapshots written by older versions fail
// decode with an explicit error; there is no in-repo migration tooling
// (postgres migrations cover the snapshot TABLE only - step-output payloads
// are opaque JSON to the store).
const AutonomousIterationStepPrefix = "auto:iter:"

// iterationEnvelopeFormat* are the on-disk formats of an iteration
// checkpoint: "delta" appends Messages to the conversation rebuilt through
// the previous boundary (Base must equal its length); "full" replaces the
// accumulated conversation, used when context compaction between boundaries
// invalidated the index arithmetic (the conversation shrank below Base).
const (
	iterationEnvelopeFormatFull  = "full"
	iterationEnvelopeFormatDelta = "delta"
)

// iterationEnvelope is the persisted form of one autonomous iteration
// boundary.
type iterationEnvelope struct {
	Format string `json:"format"`
	Base   int    `json:"base"`
	// BaseHash anchors a "delta" envelope to the CONTENT of the prefix it
	// builds on: the stable hash of the prefix's last message
	// (messages[Base-1] at persist time). A length-only base cannot tell a
	// compacted-then-regrown conversation apart from the persisted one, so
	// the writer degrades to "full" when the anchor does not match and the
	// fold side rejects a delta whose anchor disagrees with the rebuilt
	// prefix. Empty on envelopes written before the field existed; those
	// fold with the historic length-only check.
	BaseHash string `json:"base_hash,omitempty"`
	// Messages holds the appended messages for "delta" (messages[Base:] of
	// the conversation after this iteration) and the whole conversation for
	// "full".
	Messages []llm.Message `json:"messages"`
	// State snapshots the run-level loop state at this boundary (see
	// iterationRunState). Envelopes written before the field existed decode
	// with a nil State and resume with zeroed trackers, the same degradation
	// pre-checkpoint_usage snapshots get on the pause/resume path.
	State *iterationRunState `json:"state,omitempty"`
}

// iterationMessageHash returns the stable content anchor of one conversation
// message. Normalization matches what persistAutonomousIteration applies
// before marshaling, and llm.Message JSON round-trips byte-identically (the
// delta/format regression tests compare marshaled messages across a
// persist/load cycle), so the write side and the fold side agree.
func iterationMessageHash(message llm.Message) string {
	normalized := llm.NormalizeMessageToolInputs([]llm.Message{message})
	raw, err := json.Marshal(normalized[0])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// iterationRunState carries the run-level loop state at an iteration
// boundary: the same classes of state the tool-approval pause persists into
// checkpoint variables (checkpoint_tool_counts, checkpoint_usage,
// checkpoint_approvals, checkpoint_deny_count, checkpoint_steps_consumed,
// checkpoint_replan_attempts), so a crash recovery through
// ResumeAutonomousFromIteration continues token budgets, doom-loop/rate-cap
// counts, approval memory and the replan budget instead of resetting them.
// Unlike Messages it is a full snapshot at every boundary (the payloads are
// small), and resume reads only the highest envelope's State.
type iterationRunState struct {
	ToolCounts     json.RawMessage `json:"tool_counts,omitempty"`
	Usage          json.RawMessage `json:"usage,omitempty"`
	Approvals      json.RawMessage `json:"approvals,omitempty"`
	DenyCount      int             `json:"deny_count,omitempty"`
	StepsConsumed  int             `json:"steps_consumed,omitempty"`
	ReplanAttempts int             `json:"replan_attempts,omitempty"`
}

func autonomousIterationKey(iteration int) string {
	return AutonomousIterationStepPrefix + strconv.Itoa(iteration)
}

// latestAutonomousIteration returns the highest-numbered persisted iteration
// in outputs. Keys with an unparsable or non-positive suffix are ignored so a
// corrupted or foreign key never wins over a valid checkpoint.
func latestAutonomousIteration(outputs map[string]runstate.StepOutputRef) (int, runstate.StepOutputRef, bool) {
	best := 0
	var bestRef runstate.StepOutputRef
	found := false
	for key, ref := range outputs {
		if !strings.HasPrefix(key, AutonomousIterationStepPrefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(key, AutonomousIterationStepPrefix))
		if err != nil || n <= 0 {
			continue
		}
		if !found || n > best {
			best, bestRef, found = n, ref, true
		}
	}
	return best, bestRef, found
}

// HasAutonomousIterationProgress reports whether the snapshot carries at
// least one persisted autonomous iteration boundary.
func HasAutonomousIterationProgress(snapshot runstate.RunSnapshot) bool {
	_, _, ok := latestAutonomousIteration(snapshot.StepOutputs)
	return ok
}

// autonomousIterationPersistenceEnabled scopes iteration persistence to the
// autonomous execution path: workflow agent nodes (which carry a workflow
// node ID on their context) persist their own per-node step outputs, and a
// delegated sub-agent shares the parent's run snapshot, so its loop must not
// clobber the parent's iteration checkpoints.
func (e *Engine) autonomousIterationPersistenceEnabled(ctx context.Context) bool {
	if core.WorkflowNodeFromContext(ctx) != "" {
		return false
	}
	return delegationDepthFromContext(ctx) == 0
}

// persistAutonomousIteration snapshots the conversation delta at an iteration
// boundary into StepOutputs["auto:iter:<n>"]: only the messages appended since
// the previous boundary (tracked per run in e.coord.iterationBases), so the write
// stays O(1) in the iteration count instead of rewriting the full transcript
// every step. When the conversation shrank below the recorded baseline
// (context compaction between boundaries), a full snapshot is written instead
// and resume folds it as a replacement. Large payloads are externalized to
// the blob store by stepOutputRef (threshold:
// scenario.Runtime.StepOutputThreshold), so the run snapshot row stays small.
// The envelope also snapshots the run-level loop state (tool-call tracker,
// usage tracker, approval cache, deny-breaker count, step/replan budgets) so
// crash recovery through ResumeAutonomousFromIteration aligns with the
// pause/resume checkpoint semantics instead of resetting them.
// The save goes through saveSnapshotWithRetry -> saveRunSnapshot ->
// runstate.SaveWithFence, so lease fencing applies automatically and an
// optimistic-concurrency conflict with another writer (tool outputs, plan
// updates) is retried rather than lost.
func (e *Engine) persistAutonomousIteration(ctx context.Context, runID string, iteration int, messages []llm.Message, tracker *toolCallTracker, replanAttempts int) error {
	base := 0
	if v, ok := e.coord.iterationBases.Load(runID); ok {
		base, _ = v.(int)
	}
	anchor := ""
	if v, ok := e.coord.iterationAnchors.Load(runID); ok {
		anchor, _ = v.(string)
	}
	envelope := iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: messages}
	if base > 0 && base <= len(messages) {
		// The length check alone cannot detect a compacted-then-regrown
		// conversation (shorter after compaction, later past the baseline
		// again): messages[:base] would no longer be the persisted prefix
		// and the delta would fold back into a garbled conversation. Only
		// a matching content anchor proves the prefix is intact.
		if prefixHash := iterationMessageHash(messages[base-1]); prefixHash != "" && prefixHash == anchor {
			envelope = iterationEnvelope{Format: iterationEnvelopeFormatDelta, Base: base, BaseHash: prefixHash, Messages: messages[base:]}
		}
	}
	envelope.Messages = llm.NormalizeMessageToolInputs(envelope.Messages)
	state, err := e.autonomousIterationRunState(runID, tracker, iteration, replanAttempts)
	if err != nil {
		return err
	}
	envelope.State = state
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	key := autonomousIterationKey(iteration)
	ref, err := e.stepOutputRef(ctx, runID, key, raw)
	if err != nil {
		return err
	}
	if err := e.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		snapshot.StepOutputs[key] = ref
		return nil
	}); err != nil {
		return err
	}
	// Only advance the baseline after the boundary is durable: a failed save
	// must not skip messages in the next delta.
	e.coord.iterationBases.Store(runID, len(messages))
	if len(messages) > 0 {
		e.coord.iterationAnchors.Store(runID, iterationMessageHash(messages[len(messages)-1]))
	}
	return nil
}

// autonomousIterationRunState captures the run-level loop state persisted at
// every iteration boundary. It mirrors the pause checkpoint variables
// (checkpoint_tool_counts / checkpoint_usage / checkpoint_approvals /
// checkpoint_deny_count) so both resume paths restore the same state; stores
// without toolorch.RunStateExporter support simply contribute no approvals,
// as on the pause path.
func (e *Engine) autonomousIterationRunState(runID string, tracker *toolCallTracker, stepsConsumed, replanAttempts int) (*iterationRunState, error) {
	countsRaw, err := json.Marshal(tracker.ensure())
	if err != nil {
		return nil, err
	}
	usageRaw, err := json.Marshal(e.usageTrackerFor(runID))
	if err != nil {
		return nil, err
	}
	state := &iterationRunState{
		ToolCounts:     countsRaw,
		Usage:          usageRaw,
		StepsConsumed:  stepsConsumed,
		ReplanAttempts: replanAttempts,
	}
	if exporter, ok := e.tooling.approvalStore.(toolorch.RunStateExporter); ok {
		if approvalsRaw, ok := exporter.ExportRun(runID); ok {
			state.Approvals = approvalsRaw
		}
	}
	if e.tooling.denyBreaker != nil {
		if count := e.tooling.denyBreaker.ExportRun(runID); count > 0 {
			state.DenyCount = count
		}
	}
	return state, nil
}

// loadAutonomousConversation rebuilds the conversation persisted at iteration
// boundaries 1..through by folding their envelopes in order: "full" replaces
// the accumulated prefix, "delta" appends. It also returns the run-level loop
// state snapshot carried by the last (highest) envelope - nil for envelopes
// written before the state field existed.
//
// A missing boundary key is tolerated as a pause hole: a tool-approval pause
// returns before the boundary persist of its step, so the paused step's
// number is absent from the chain while later boundaries exist. Integrity is
// enforced by content, not key contiguity — a "full" envelope re-anchors the
// chain regardless of earlier gaps, and a "delta" must match the rebuilt
// prefix in length (Base) and, when it carries one, content (BaseHash). A gap
// the following delta does not span, a baseline mismatch, or an anchor
// mismatch means the checkpoint chain is genuinely corrupt (or predates the
// envelope format) and is reported as an error instead of resuming from a
// truncated conversation.
func (e *Engine) loadAutonomousConversation(ctx context.Context, runID string, outputs map[string]runstate.StepOutputRef, through int) ([]llm.Message, *iterationRunState, error) {
	var messages []llm.Message
	var state *iterationRunState
	for i := 1; i <= through; i++ {
		key := autonomousIterationKey(i)
		ref, ok := outputs[key]
		if !ok {
			// Pause hole (see the doc comment): skip; the next envelope's
			// full-reset or delta base/anchor check decides consistency.
			continue
		}
		raw, err := runstate.LoadStepOutput(ctx, e.persist.blobs, ref)
		if err != nil {
			return nil, nil, fmt.Errorf("runtime: load persisted iteration %d for run %q: %w", i, runID, err)
		}
		var envelope iterationEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, nil, fmt.Errorf("runtime: decode persisted iteration %d for run %q (legacy full-conversation checkpoints are no longer supported): %w", i, runID, err)
		}
		switch envelope.Format {
		case iterationEnvelopeFormatFull:
			messages = append([]llm.Message(nil), envelope.Messages...)
		case iterationEnvelopeFormatDelta:
			if envelope.Base != len(messages) {
				return nil, nil, fmt.Errorf("runtime: persisted iteration %d for run %q has base %d, but the rebuilt conversation has %d messages", i, runID, envelope.Base, len(messages))
			}
			if envelope.BaseHash != "" {
				if len(messages) == 0 || iterationMessageHash(messages[len(messages)-1]) != envelope.BaseHash {
					return nil, nil, fmt.Errorf("runtime: persisted iteration %d for run %q has a base anchor mismatch: the rebuilt prefix does not match the conversation the delta was written against", i, runID)
				}
			}
			messages = append(messages, envelope.Messages...)
		default:
			return nil, nil, fmt.Errorf("runtime: persisted iteration %d for run %q has unknown format %q", i, runID, envelope.Format)
		}
		state = envelope.State
	}
	return messages, state, nil
}

// ResumeAutonomousFromIteration re-enters an autonomous run that failed
// mid-loop without a HITL gate checkpoint. It loads the conversation
// persisted at the highest iteration boundary and continues the tool loop
// from there: completed iterations are never re-sent to the LLM.
//
// The resumed loop restores the run-level loop state snapshot persisted at
// the highest boundary (tool-call tracker, usage tracker, approval cache,
// deny-breaker count, step/replan budgets), so crash recovery keeps the same
// governance and budget semantics as a pause/resume cycle. Envelopes written
// before the state field existed carry none and resume with zeroed trackers,
// the pre-state behavior.
//
// Side-effect semantics are at-least-once at iteration granularity: a crash
// after an iteration's tool calls executed but before the boundary was
// persisted replays that whole iteration (the LLM call included) on resume.
// Tools with side effects should deduplicate on the run-scoped idempotency
// key the runtime passes through the tool-call context.
func (e *Engine) ResumeAutonomousFromIteration(ctx context.Context, runID string) (RunResult, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return RunResult{}, fmt.Errorf("runtime: iteration resume requires running snapshot, got %s", snapshot.Status)
	}
	iteration, _, ok := latestAutonomousIteration(snapshot.StepOutputs)
	if !ok {
		return RunResult{}, fmt.Errorf("runtime: run %q has no persisted autonomous iteration progress", runID)
	}
	messages, state, err := e.loadAutonomousConversation(ctx, runID, snapshot.StepOutputs, iteration)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, err)
	}
	if len(messages) == 0 {
		return RunResult{}, e.failContinuePermanent(ctx, runID, fmt.Errorf("runtime: persisted iteration messages for run %q are empty", runID))
	}
	// The resumed loop keeps appending to this conversation: seed the delta
	// baseline (length + content anchor) so the next boundary persists only
	// new messages.
	e.coord.iterationBases.Store(runID, len(messages))
	e.coord.iterationAnchors.Store(runID, iterationMessageHash(messages[len(messages)-1]))
	if mode := TrustMode(variableString(snapshot.Variables, resumeTrustModeVar)); mode != "" {
		ctx = ContextWithTrustMode(ctx, mode)
	}
	agent, err := e.resolveAgent(variableString(snapshot.Variables, resumeAgentVar))
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, err)
	}
	if e.llm == nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, ErrLLMGatewayRequired)
	}
	profile, err := e.llmProfile(agent.LLM)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, err)
	}
	caller, ok := e.llm.(llm.ToolCaller)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
		return RunResult{}, e.failContinuePermanent(ctx, runID, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM))
	}
	tracker, stepsConsumed, replanAttempts, err := e.restoreIterationRunState(ctx, runID, state, iteration)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, err)
	}
	prompt := variableString(snapshot.Variables, resumePromptVar)
	output, err := e.continueToolLoopFrom(ctx, runID, agent, profile, messages, nil, tracker, caller, prompt, "", stepsConsumed, replanAttempts)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		if isPermanentContinueError(err) {
			return RunResult{}, e.failContinuePermanent(ctx, runID, err)
		}
		// Transient failure: the iteration checkpoints stay intact and the
		// run stays Running so the caller (or the reaper, after it marks the
		// run Failed again) can retry the resume.
		return RunResult{}, err
	}
	return e.completeRun(ctx, runID, output)
}

// restoreIterationRunState rebuilds the run-level loop state from the highest
// envelope's snapshot, mirroring what continueToolApproval restores from the
// pause checkpoint variables: the doom-loop/rate-cap tracker, the usage
// tracker (token budget + context-recovery attempts), the approval cache and
// deny-breaker count, and the cumulative step/replan budgets. A nil state (an
// envelope written before the field existed) keeps the pre-state behavior: a
// fresh tracker, stepsConsumed = iteration, replanAttempts = 0.
func (e *Engine) restoreIterationRunState(ctx context.Context, runID string, state *iterationRunState, iteration int) (*toolCallTracker, int, int, error) {
	tracker := newToolCallTracker()
	stepsConsumed := iteration
	replanAttempts := 0
	if state == nil {
		return tracker, stepsConsumed, replanAttempts, nil
	}
	if len(state.ToolCounts) > 0 {
		decoded, err := decodeToolCallTracker(state.ToolCounts)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("runtime: decode iteration tool counts: %w", err)
		}
		tracker = decoded
	}
	usage, err := decodeUsageTracker(state.Usage)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("runtime: decode iteration usage: %w", err)
	}
	e.restoreUsageTracker(runID, usage)
	// Reuse the pause-path restore for the approval cache and deny-breaker
	// count so both resume paths share one import implementation.
	vars := make(map[string]json.RawMessage, 2)
	if len(state.Approvals) > 0 {
		vars[checkpointApprovalsVar] = state.Approvals
	}
	if state.DenyCount > 0 {
		vars[checkpointDenyCountVar] = json.RawMessage(strconv.Itoa(state.DenyCount))
	}
	e.restoreApprovalCheckpointState(ctx, runID, vars)
	if state.StepsConsumed > 0 {
		stepsConsumed = state.StepsConsumed
	}
	replanAttempts = state.ReplanAttempts
	return tracker, stepsConsumed, replanAttempts, nil
}
