package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// terminationReasonForError classifies a terminal failure cause into the
// core.TerminationReason* vocabulary written to the RunFailed payload.
// Order matters: lease-ownership failures outrank everything (a cancelled
// context may wrap them), and a provider APIError outranks the generic
// bucket so model/API failures stay distinguishable from runtime bugs.
func terminationReasonForError(err error) string {
	switch {
	case errors.Is(err, ErrMaxStepsExceeded):
		return core.TerminationReasonMaxStepsExceeded
	case errors.Is(err, ErrTokenBudgetExceeded):
		return core.TerminationReasonBudgetExceeded
	case errors.Is(err, context.DeadlineExceeded):
		return core.TerminationReasonTimeout
	case errors.Is(err, runstate.ErrStaleFence), errors.Is(err, coordination.ErrRunLeaseLost):
		return core.TerminationReasonLeaseLost
	default:
		// A failure that carries its own attribution (e.g. a feature stop
		// condition) wins over the generic buckets.
		var reasoned interface{ TerminationReason() string }
		if errors.As(err, &reasoned) {
			if reason := reasoned.TerminationReason(); reason != "" {
				return reason
			}
		}
		var apiErr llm.APIError
		if errors.As(err, &apiErr) {
			return core.TerminationReasonLLMError
		}
		return core.TerminationReasonError
	}
}

// markRunFailedOrCancelled classifies err and persists the run as Cancelled
// when it stems from the caller's context being explicitly cancelled, or as
// Failed otherwise (a deadline timeout is still a genuine failure). A
// cancellation whose cause is a lost run lease — or a save rejected with
// ErrStaleFence, which means the same thing: a newer lease holder owns the
// run — is a worker-ownership failure, never a caller cancel: it is
// persisted as Failed with the lease-lost reason. This mirrors the
// classification Stream already applies to its tool-loop goroutine, so a
// caller-initiated cancellation is never recorded - and counted in metrics -
// as a run failure.
func (e *Engine) markRunFailedOrCancelled(ctx context.Context, runID string, err error) {
	if cause := context.Cause(ctx); cause != nil && errors.Is(cause, coordination.ErrRunLeaseLost) {
		e.markRunFailedLease(ctx, runID, cause)
		return
	}
	if errors.Is(err, coordination.ErrRunLeaseLost) {
		e.markRunFailedLease(ctx, runID, err)
		return
	}
	if errors.Is(err, runstate.ErrStaleFence) {
		e.markRunFailedLease(ctx, runID, err)
		return
	}
	if errors.Is(err, context.Canceled) {
		e.markRunCancelled(ctx, runID)
		return
	}
	e.markRunFailed(ctx, runID, err)
}

func (e *Engine) completeStreamRun(ctx context.Context, runID string, agent core.Agent, prompt string, output string) error {
	loaded, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
	if err != nil {
		return err
	}
	if loaded.Status != runstate.RunStatusRunning {
		// The run was paused, cancelled, or failed by a concurrent writer
		// while this stream was still producing output; do not clobber
		// that terminal/paused state with Completed.
		return fmt.Errorf("runtime: cannot complete run %q in status %s", runID, loaded.Status)
	}
	// Tool loops persist user/assistant/tool messages incrementally inside
	// answerWithTools; only plain chat streams need a final memory write
	// here. Write memory before persisting the terminal Completed status so
	// a memory failure never leaves the run marked complete with missing
	// history.
	if len(agent.Tools) == 0 && len(agent.SubAgents) == 0 {
		if err := e.writeMemory(ctx, runID, agent, []memoryMessage{
			runTurnMemoryMessage(string(llm.RoleUser), prompt),
			runTurnMemoryMessage(string(llm.RoleAssistant), output),
		}); err != nil {
			return err
		}
	}
	finalRaw, err := json.Marshal(map[string]string{"text": output})
	if err != nil {
		return fmt.Errorf("runtime: marshal streamed final output: %w", err)
	}
	if _, err := e.persistRunCompleted(ctx, runID, finalRaw); err != nil {
		var conflict completionConflictError
		if errors.As(err, &conflict) {
			return fmt.Errorf("runtime: cannot complete run %q in status %s", runID, conflict.status)
		}
		e.markRunFailedOrCancelled(ctx, runID, err)
		return err
	}
	return nil
}

// runDuration reports how long a run has been executing, preferring the
// run_started_at variable stamped by beginRun (most precise: it survives
// resumes and always reflects the true first Running transition), then
// falling back to the snapshot's CreatedAt - which every runstate
// repository implementation stamps via StampSnapshot regardless of which
// code path created the run, including workflow/hybrid snapshots created
// directly by the framework layer rather than through beginRun. Returns 0
// if neither is available so callers can skip the histogram observation
// rather than record a meaningless zero-based duration.
func runDuration(snapshot runstate.RunSnapshot) time.Duration {
	if startedAt := variableString(snapshot.Variables, runStartedAtVar); startedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
			return time.Since(parsed)
		}
	}
	if !snapshot.CreatedAt.IsZero() {
		return time.Since(snapshot.CreatedAt)
	}
	return 0
}

// recordRunCompleted records the duration histogram and completed-event
// counter for a finished run. Only Run() used to record these; every other
// completion path (RunStructured, RunHybrid, completeRun, completeStreamRun)
// silently skipped them, leaving gaps in dashboards built on these metrics.
func (e *Engine) recordRunCompleted(ctx context.Context, snapshot runstate.RunSnapshot) {
	if d := runDuration(snapshot); d > 0 {
		e.obs.recorder.ObserveHistogram(ctx, observability.MetricRunDurationSeconds, d.Seconds(),
			observability.Attribute{Key: "scenario", Value: e.scenario.Name})
	}
	e.obs.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
		observability.Attribute{Key: "event", Value: string(core.EventRunCompleted)},
		observability.Attribute{Key: "scenario", Value: e.scenario.Name})
}

// completionConflictError reports that a completion save observed the run in
// a non-Running status: a concurrent writer (pause, cancellation, failure)
// got there first and its state must not be clobbered with Completed.
type completionConflictError struct {
	status runstate.RunStatus
}

func (e completionConflictError) Error() string {
	return fmt.Sprintf("runtime: run is not running (status=%s)", e.status)
}

// persistRunCompleted writes the terminal Completed status together with the
// "final" step output, retrying optimistic-concurrency conflicts by
// reloading and re-checking on every attempt that the run is still Running.
// A bare runs.Save here used to give up on the first ErrStaleSnapshot,
// leaving the run stuck in Running with its produced answer lost.
func (e *Engine) persistRunCompleted(ctx context.Context, runID string, finalRaw json.RawMessage) (runstate.RunSnapshot, error) {
	finalRef, err := e.stepOutputRef(ctx, runID, "final", finalRaw)
	if err != nil {
		return runstate.RunSnapshot{}, err
	}
	var saved runstate.RunSnapshot
	if err := e.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Status != runstate.RunStatusRunning {
			return completionConflictError{status: snapshot.Status}
		}
		snapshot.Status = runstate.RunStatusCompleted
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		snapshot.StepOutputs["final"] = finalRef
		saved = *snapshot
		return nil
	}); err != nil {
		if finalRef.Blob != nil {
			if blobs, ok := e.persist.blobs.(runstate.BlobAdmin); ok {
				if deleteErr := blobs.Delete(ctx, *finalRef.Blob); deleteErr != nil {
					e.logWarn(ctx, "runtime: failed to delete orphaned final output blob", "run_id", runID, "error", deleteErr)
				}
			}
		}
		return runstate.RunSnapshot{}, err
	}
	e.clearRunScopedState(runID)
	e.notifyRunStatusSettled(runID)
	e.recordRunCompleted(ctx, saved)
	var eventFields map[string]json.RawMessage
	if err := json.Unmarshal(finalRaw, &eventFields); err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("runtime: decode final output for completion event: %w", err)
	}
	if eventFields == nil {
		eventFields = make(map[string]json.RawMessage)
	}
	refRaw, err := json.Marshal(finalRef)
	if err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("runtime: marshal final output reference: %w", err)
	}
	eventFields["output_ref"] = refRaw
	eventPayload, err := json.Marshal(eventFields)
	if err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("runtime: marshal completion event payload: %w", err)
	}
	e.emit(ctx, core.EventRunCompleted, runID, eventPayload)
	return saved, nil
}

// nonRunningCompletionResult builds the RunResult/error pair to return when
// a completion path discovers the run is no longer Running: some other
// concurrent writer already moved it to Paused/Cancelled/Failed between the
// answer producing its output and this reload. Reporting Paused/Cancelled/
// Failed as a structured RunResult - the same way this engine already
// reports a pause discovered synchronously within a single call - lets
// callers branch on RunResult.Status instead of receiving an opaque
// "cannot complete" error that hides which terminal state the run actually
// ended up in.
func nonRunningCompletionResult(runID string, status runstate.RunStatus) (RunResult, error) {
	switch status {
	case runstate.RunStatusPaused, runstate.RunStatusCancelled, runstate.RunStatusFailed:
		return RunResult{RunID: runID, Status: status}, nil
	default:
		return RunResult{}, fmt.Errorf("runtime: cannot complete run %q in status %s", runID, status)
	}
}

func (e *Engine) markRunFailed(ctx context.Context, runID string, cause error) {
	// A stale-fence failure means a newer lease holder owns the run: it
	// settles exactly like a lost lease, forcing past tool-approval
	// checkpoint preservation so the run still reaches Failed (or is left
	// for the reaper when the fence rejects even that write).
	e.markRunFailedMode(ctx, runID, cause, errors.Is(cause, runstate.ErrStaleFence))
}

// markRunFailedLease persists a lease-lost failure. Unlike ordinary failures
// it does not spare a snapshot carrying tool-approval checkpoint metadata:
// losing the lease means no worker will drive a pending resume, so the run
// must still reach Failed (the checkpoint stays intact for RetryFailedRun /
// ContinueRun to re-enter from).
func (e *Engine) markRunFailedLease(ctx context.Context, runID string, cause error) {
	e.markRunFailedMode(ctx, runID, cause, true)
}

// markRunFailedPermanent persists a permanent continue failure. Like the
// lease-lost variant it forces past the tool-approval checkpoint
// preservation: a permanent error (missing gateway, corrupt checkpoint
// metadata, unconfigured agent/profile) can never succeed on a blind retry,
// so the run must reach Failed instead of lingering in Running. The
// checkpoint variables themselves are intentionally kept: once the
// underlying configuration is fixed, RetryFailedRun / ContinueRun re-enter
// from them.
func (e *Engine) markRunFailedPermanent(ctx context.Context, runID string, cause error) {
	e.markRunFailedMode(ctx, runID, cause, true)
}

func (e *Engine) markRunFailedMode(ctx context.Context, runID string, cause error, force bool) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	// Notify after the persistence attempt regardless of outcome: on success
	// this process settled the run; on retry exhaustion the competing writer
	// that won the CAS race may have settled it instead.
	defer e.notifyRunStatusSettled(runID)
	defer e.clearRunScopedState(runID)
	var status runstate.RunStatus
	if err := e.saveSnapshotWithRetry(persistCtx, runID, func(snapshot *runstate.RunSnapshot) error {
		status = snapshot.Status
		if snapshot.Status == runstate.RunStatusCancelled {
			return nil
		}
		if snapshot.Status == runstate.RunStatusPaused || (!force && snapshotHasToolApprovalCheckpoint(snapshot)) {
			return nil
		}
		if snapshot.Status != runstate.RunStatusFailed && !snapshot.Status.CanTransitionTo(runstate.RunStatusFailed) {
			e.logWarn(persistCtx, "runtime: refusing invalid failure status transition", "run_id", runID, "status", snapshot.Status)
			return nil
		}
		snapshot.Status = runstate.RunStatusFailed
		status = snapshot.Status
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		// Persist the failure reason on the snapshot itself, not just in
		// the emitted event: reloading a failed run later (e.g. from a
		// separate diagnostic tool, or after the event bus has rotated old
		// events out) would otherwise give no indication of why it failed.
		raw, err := json.Marshal(cause.Error())
		if err != nil {
			return fmt.Errorf("runtime: marshal run failure reason: %w", err)
		}
		snapshot.Variables[runErrorMessageVar] = raw
		return nil
	}); err != nil {
		// Retry exhaustion is never force-overwritten: a writer that
		// keeps winning the CAS race for all jittered attempts is actively
		// advancing this run (step outputs, pause, cancellation), and a blind
		// terminal write could clobber a legitimate Paused/Cancelled state —
		// a worse outcome than a Running run that a reaper (or, for leased
		// runs, the fence) can still recover. ErrStaleFence passes through
		// unretried inside saveSnapshotWithRetry and is handled separately
		// (a superseded lease means a new owner will settle the run).
		//
		// The run is now stuck in Running while this worker is done with it.
		// The engine cannot release the run lease itself — the lease handle
		// (locker + fencing token) is owned by the facade's holdRunLease;
		// the engine only sees owner/token as context values — so instead it
		// stamps the terminal_persist_failed snapshot variable (best-effort,
		// still optimistic CAS) for the reaper/operator inspection to
		// recognize "worker finished but could not settle", and raises the
		// signal to error level plus a diagnostic event.
		e.handleTerminalPersistExhausted(persistCtx, runID, runstate.RunStatusFailed, err)
		return
	}
	if status == runstate.RunStatusCancelled {
		e.emitJSON(persistCtx, core.EventRunCancelled, runID, map[string]string{"termination_reason": core.TerminationReasonCancelled})
		return
	}
	if status != runstate.RunStatusFailed {
		return
	}
	e.emitJSON(persistCtx, core.EventRunFailed, runID, map[string]string{
		"error":              cause.Error(),
		"termination_reason": terminationReasonForError(cause),
	})
}

func (e *Engine) markRunCancelled(ctx context.Context, runID string) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	defer e.notifyRunStatusSettled(runID)
	defer e.clearRunScopedState(runID)
	var status runstate.RunStatus
	if err := e.saveSnapshotWithRetry(persistCtx, runID, func(snapshot *runstate.RunSnapshot) error {
		status = snapshot.Status
		if snapshot.Status != runstate.RunStatusCancelled {
			if !snapshot.Status.CanTransitionTo(runstate.RunStatusCancelled) {
				e.logWarn(persistCtx, "runtime: refusing invalid cancellation status transition", "run_id", runID, "status", snapshot.Status)
				return nil
			}
			snapshot.Status = runstate.RunStatusCancelled
			status = snapshot.Status
		}
		return nil
	}); err != nil {
		// Same trade-off as markRunFailedMode above: never force-write over an
		// actively advancing concurrent writer; stamp terminal_persist_failed
		// and escalate the signal instead (see handleTerminalPersistExhausted).
		e.handleTerminalPersistExhausted(persistCtx, runID, runstate.RunStatusCancelled, err)
		return
	}
	if status != runstate.RunStatusCancelled {
		return
	}
	// A cancelled run still gets a structured terminal payload (never nil):
	// downstream consumers key off termination_reason=cancelled without
	// having to nil-check the payload first.
	e.emitJSON(persistCtx, core.EventRunCancelled, runID, map[string]string{"termination_reason": core.TerminationReasonCancelled})
}

// handleTerminalPersistExhausted is the fallback when settling a run to a
// terminal status (failed/cancelled) exhausted every jittered CAS retry: the
// run is left in Running while this worker is done executing it, and a reaper
// will not reap it while this process still holds (and renews) the lease.
//
// Releasing the lease from here is not possible with the current structure:
// the lease handle (locker + fencing token) is owned by the framework
// facade's holdRunLease renewal loop — the engine only sees the owner and
// token as context values, never the locker — so the minimal safe substitute
// is used instead: an error-level log, a RunTerminalPersistFailed diagnostic
// event, and a best-effort terminal_persist_failed snapshot variable so the
// reaper / operator inspection can distinguish "worker finished but could
// not settle" from a genuinely live run. The marker write is an ordinary
// optimistic CAS save, never a force-write: if it collides again the run
// simply stays unmarked.
func (e *Engine) handleTerminalPersistExhausted(ctx context.Context, runID string, target runstate.RunStatus, saveErr error) {
	if errors.Is(saveErr, runstate.ErrStaleFence) {
		// A newer lease holder owns the run and settles it; nothing is stuck
		// and any write from this worker (including the marker) is rejected
		// by the fence anyway.
		e.logWarn(ctx, "runtime: terminal status persistence rejected by a superseded lease fence", "run_id", runID, "target_status", string(target))
		return
	}
	e.logError(ctx, "runtime: terminal status persistence retries exhausted; run left Running", "run_id", runID, "target_status", string(target), "save_error", saveErr)
	e.emitJSON(ctx, core.EventRunTerminalPersistFailed, runID, map[string]string{
		"target_status": string(target),
		"save_error":    saveErr.Error(),
	})
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
	if err != nil {
		e.logWarn(ctx, "runtime: failed to reload run for terminal-persist-failed marker", "run_id", runID, "error", err)
		return
	}
	if snapshot.Status != runstate.RunStatusRunning {
		// The competing writer settled the run after all; no marker needed.
		return
	}
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	snapshot.Variables[runstate.VarTerminalPersistFailed] = jsonStringValue(string(target))
	if err := e.saveRunSnapshot(ctx, &snapshot, snapshot.Version); err != nil {
		e.logWarn(ctx, "runtime: failed to stamp terminal-persist-failed marker", "run_id", runID, "error", err)
	}
}

// clearRunScopedState drops every run-keyed in-memory bookkeeping entry
// (cached approval decisions, deny-breaker counters, buffered interjections)
// once a run reaches a terminal state, so a long-lived worker does not
// accumulate stale entries for runs that will never execute again.
func (e *Engine) clearRunScopedState(runID string) {
	if e.tooling.approvalStore != nil {
		e.tooling.approvalStore.Clear(runID)
	}
	if e.tooling.denyBreaker != nil {
		e.tooling.denyBreaker.Clear(runID)
	}
	e.clearInterjections(runID)
	e.coord.loadedTools.Delete(runID)
	e.coord.pendingSelfCompact.Delete(runID)
	e.coord.usageTrackers.Delete(runID)
	e.coord.iterationBases.Delete(runID)
	e.coord.iterationAnchors.Delete(runID)
	e.coord.toolArgsRepairs.Delete(runID)
}

// ClearRunScopedState exposes clearRunScopedState for the framework facade,
// whose workflow-mode terminal transitions (completeWorkflowRun,
// markWorkflowFailed) persist outside the engine's own terminal helpers but
// share the same run-scoped bookkeeping.
func (e *Engine) ClearRunScopedState(runID string) {
	e.clearRunScopedState(runID)
}

// MarkRunCancelled exposes the engine's terminal cancellation transition to
// facade-owned workflow execution paths.
func (e *Engine) MarkRunCancelled(ctx context.Context, runID string) {
	e.markRunCancelled(ctx, runID)
}

func snapshotHasToolApprovalCheckpoint(snapshot *runstate.RunSnapshot) bool {
	if snapshot == nil || snapshot.Variables == nil {
		return false
	}
	raw, ok := snapshot.Variables[checkpointKindVar]
	if !ok {
		return false
	}
	var kind string
	if err := json.Unmarshal(raw, &kind); err != nil {
		return false
	}
	return kind == checkpointKindToolApproval
}

func (e *Engine) completeRun(ctx context.Context, runID, output string) (RunResult, error) {
	finalRaw, err := json.Marshal(map[string]string{"text": output})
	if err != nil {
		return RunResult{}, fmt.Errorf("runtime: marshal final output: %w", err)
	}
	if _, err := e.persistRunCompleted(ctx, runID, finalRaw); err != nil {
		var conflict completionConflictError
		if errors.As(err, &conflict) {
			return nonRunningCompletionResult(runID, conflict.status)
		}
		// The run produced its answer but the terminal state could not be
		// persisted; mark it Failed so it does not linger in Running forever.
		e.markRunFailedOrCancelled(ctx, runID, err)
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: output}, nil
}
