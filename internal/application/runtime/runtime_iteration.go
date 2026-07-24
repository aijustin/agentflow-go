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
// "auto:iter:<n>" is the JSON-serialized conversation ([]llm.Message) after
// iteration n completed - one LLM response plus every tool call it requested
// - externalized to the blob store above the step-output threshold like any
// other step output. RetryFailedRun resumes a crashed autonomous run from the
// highest persisted iteration instead of requiring a HITL gate checkpoint.
const AutonomousIterationStepPrefix = "auto:iter:"

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

// persistAutonomousIteration snapshots the conversation at an iteration
// boundary into StepOutputs["auto:iter:<n>"]. Large conversations are
// externalized to the blob store by stepOutputRef (threshold:
// scenario.Runtime.StepOutputThreshold), so the run snapshot row stays small.
// The save goes through saveSnapshotWithRetry -> saveRunSnapshot ->
// runstate.SaveWithFence, so lease fencing applies automatically and an
// optimistic-concurrency conflict with another writer (tool outputs, plan
// updates) is retried rather than lost.
func (e *Engine) persistAutonomousIteration(ctx context.Context, runID string, iteration int, messages []llm.Message) error {
	raw, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	key := autonomousIterationKey(iteration)
	ref, err := e.stepOutputRef(ctx, runID, key, raw)
	if err != nil {
		return err
	}
	return e.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		snapshot.StepOutputs[key] = ref
		return nil
	})
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return RunResult{}, fmt.Errorf("runtime: iteration resume requires running snapshot, got %s", snapshot.Status)
	}
	iteration, ref, ok := latestAutonomousIteration(snapshot.StepOutputs)
	if !ok {
		return RunResult{}, fmt.Errorf("runtime: run %q has no persisted autonomous iteration progress", runID)
	}
	raw, err := runstate.LoadStepOutput(ctx, e.blobs, ref)
	if err != nil {
		return RunResult{}, fmt.Errorf("runtime: load persisted iteration messages for run %q: %w", runID, err)
	}
	var messages []llm.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, runID, fmt.Errorf("runtime: decode persisted iteration messages: %w", err))
	}
	if len(messages) == 0 {
		return RunResult{}, e.failContinuePermanent(ctx, runID, fmt.Errorf("runtime: persisted iteration messages for run %q are empty", runID))
	}
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
