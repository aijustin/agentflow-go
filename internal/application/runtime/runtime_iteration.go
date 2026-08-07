package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
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
	// Messages holds the appended messages for "delta" (messages[Base:] of
	// the conversation after this iteration) and the whole conversation for
	// "full".
	Messages []llm.Message `json:"messages"`
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
// The save goes through saveSnapshotWithRetry -> saveRunSnapshot ->
// runstate.SaveWithFence, so lease fencing applies automatically and an
// optimistic-concurrency conflict with another writer (tool outputs, plan
// updates) is retried rather than lost.
func (e *Engine) persistAutonomousIteration(ctx context.Context, runID string, iteration int, messages []llm.Message) error {
	base := 0
	if v, ok := e.coord.iterationBases.Load(runID); ok {
		base, _ = v.(int)
	}
	envelope := iterationEnvelope{Format: iterationEnvelopeFormatFull, Messages: messages}
	if base > 0 && base <= len(messages) {
		envelope = iterationEnvelope{Format: iterationEnvelopeFormatDelta, Base: base, Messages: messages[base:]}
	}
	envelope.Messages = llm.NormalizeMessageToolInputs(envelope.Messages)
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
	return nil
}

// loadAutonomousConversation rebuilds the conversation persisted at iteration
// boundaries 1..through by folding their envelopes in order: "full" replaces
// the accumulated prefix, "delta" appends. A missing boundary or a baseline
// mismatch means the checkpoint chain is corrupt (or predates the envelope
// format) and is reported as an error instead of resuming from a truncated
// conversation.
func (e *Engine) loadAutonomousConversation(ctx context.Context, runID string, outputs map[string]runstate.StepOutputRef, through int) ([]llm.Message, error) {
	var messages []llm.Message
	for i := 1; i <= through; i++ {
		key := autonomousIterationKey(i)
		ref, ok := outputs[key]
		if !ok {
			return nil, fmt.Errorf("runtime: run %q is missing persisted iteration %d (checkpoint chain has a gap)", runID, i)
		}
		raw, err := runstate.LoadStepOutput(ctx, e.persist.blobs, ref)
		if err != nil {
			return nil, fmt.Errorf("runtime: load persisted iteration %d for run %q: %w", i, runID, err)
		}
		var envelope iterationEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("runtime: decode persisted iteration %d for run %q (legacy full-conversation checkpoints are no longer supported): %w", i, runID, err)
		}
		switch envelope.Format {
		case iterationEnvelopeFormatFull:
			messages = append([]llm.Message(nil), envelope.Messages...)
		case iterationEnvelopeFormatDelta:
			if envelope.Base != len(messages) {
				return nil, fmt.Errorf("runtime: persisted iteration %d for run %q has base %d, but the rebuilt conversation has %d messages", i, runID, envelope.Base, len(messages))
			}
			messages = append(messages, envelope.Messages...)
		default:
			return nil, fmt.Errorf("runtime: persisted iteration %d for run %q has unknown format %q", i, runID, envelope.Format)
		}
	}
	return messages, nil
}

// ResumeAutonomousFromIteration re-enters an autonomous run that failed
// mid-loop without a HITL gate checkpoint. It loads the conversation
// persisted at the highest iteration boundary and continues the tool loop
// from there: completed iterations are never re-sent to the LLM.
//
// The resumed loop starts with a fresh tool-call tracker and replan budget:
// neither is part of the iteration checkpoint, so governance rate caps and
// the replan limit reset across a crash recovery, same as a process restart.
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
	messages, err := e.loadAutonomousConversation(ctx, runID, snapshot.StepOutputs, iteration)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, err)
	}
	if len(messages) == 0 {
		return RunResult{}, e.failContinuePermanent(ctx, runID, fmt.Errorf("runtime: persisted iteration messages for run %q are empty", runID))
	}
	// The resumed loop keeps appending to this conversation: seed the delta
	// baseline so the next boundary persists only new messages.
	e.coord.iterationBases.Store(runID, len(messages))
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
	prompt := variableString(snapshot.Variables, resumePromptVar)
	output, err := e.continueToolLoopFrom(ctx, runID, agent, profile, messages, nil, newToolCallTracker(), caller, prompt, "", iteration, 0)
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
