package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/retry"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// ensureRunPaused persists the Paused status after gate.Pause issued a
// token, retrying stale-version conflicts the same way saveSnapshotWithRetry
// does: a concurrent snapshot advance between the load and the CAS save must
// not strand the run in Running while a pause token is already out. A gate
// that persisted the transition itself (the built-in gates do) makes this a
// no-op — the reload observes the run is no longer Running and returns
// without writing, because rewriting an already-Paused snapshot would
// advance the version and supersede the pause token the gate just issued
// (ErrTokenSuperseded on resume).
func (e *Engine) ensureRunPaused(ctx context.Context, runID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
		if err != nil {
			return err
		}
		if snapshot.Status != runstate.RunStatusRunning {
			e.notifyRunStatusSettled(runID)
			return nil
		}
		snapshot.Status = runstate.RunStatusPaused
		if err := e.saveRunSnapshot(ctx, &snapshot, snapshot.Version); err != nil {
			// ErrStaleFence passes straight through: a newer lease holder owns
			// the run, so retrying can never succeed (see
			// saveSnapshotWithRetry).
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				if delayErr := retryDelay(ctx, attempt); delayErr != nil {
					return delayErr
				}
				continue
			}
			return err
		}
		e.notifyRunStatusSettled(runID)
		return nil
	}
	return fmt.Errorf("runtime: failed to persist paused status for run %q after stale retries", runID)
}

func (e *Engine) saveStepOutput(ctx context.Context, runID, key string, value any) error {
	if e.persist.runs == nil {
		return fmt.Errorf("runtime: runstate repository is required to save step output")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Retry on stale snapshot so concurrent writers to the same run (tool
	// outputs, plan updates) do not lose this step output via optimistic
	// concurrency conflicts, matching the orchestration saveStepOutput.
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		ref, err := e.stepOutputRef(ctx, runID, key, raw)
		if err != nil {
			return err
		}
		snapshot.StepOutputs[key] = ref
		err = e.saveRunSnapshot(ctx, &snapshot, snapshot.Version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return err
		}
	}
	return fmt.Errorf("runtime: failed to save step %q output after stale snapshot retries", key)
}

// saveStepOutputs persists several step outputs in a single optimistic
// compare-and-swap round: one Load, one ref per key, one Save. The parallel
// tool batch uses it to collapse what would otherwise be N concurrent
// saveStepOutput writers on the same run snapshot (an optimistic-CAS storm)
// into one writer; retries still reload on ErrStaleSnapshot so other
// concurrent writers (plan updates, memory reconcile) do not silently lose
// these outputs.
func (e *Engine) saveStepOutputs(ctx context.Context, runID string, outputs map[string]any) error {
	if len(outputs) == 0 {
		return nil
	}
	if e.persist.runs == nil {
		return fmt.Errorf("runtime: runstate repository is required to save step outputs")
	}
	raws := make(map[string]json.RawMessage, len(outputs))
	for key, value := range outputs {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raws[key] = raw
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		for key, raw := range raws {
			ref, err := e.stepOutputRef(ctx, runID, key, raw)
			if err != nil {
				return err
			}
			snapshot.StepOutputs[key] = ref
		}
		err = e.saveRunSnapshot(ctx, &snapshot, snapshot.Version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return err
		}
	}
	return fmt.Errorf("runtime: failed to save %d step outputs after stale snapshot retries", len(outputs))
}

// stampLeaseOwner records the context's lease owner on the snapshot so a
// later reaper can tell lease-managed runs from unmanaged ones.
func stampLeaseOwner(ctx context.Context, snapshot *runstate.RunSnapshot) {
	owner := RunLeaseOwnerFromContext(ctx)
	if owner == "" || snapshot == nil {
		return
	}
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	snapshot.Variables[runstate.VarRunLeaseOwner] = jsonStringValue(owner)
}

func (e *Engine) saveSnapshotWithRetry(ctx context.Context, runID string, mutate func(*runstate.RunSnapshot) error) error {
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
		if err != nil {
			return err
		}
		if mutate != nil {
			if err := mutate(&snapshot); err != nil {
				return err
			}
		}
		if err := e.saveRunSnapshot(ctx, &snapshot, snapshot.Version); err != nil {
			// ErrStaleFence passes straight through: the run's lease was
			// superseded by a newer holder, so retrying can never succeed
			// and must not race the new owner's writes.
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				// Back off with jitter before re-colliding on the same version
				// so concurrent writers serialize instead of stampeding.
				if delayErr := retryDelay(ctx, attempt); delayErr != nil {
					return delayErr
				}
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("runtime: failed to save snapshot %q after stale retries", runID)
}

// saveRunSnapshot persists a run snapshot with lease fencing: when the
// context carries a fence token (the run executes under WithRunLease) and
// the repository implements runstate.FencedRepository, the save presents
// the token and a superseded writer fails with runstate.ErrStaleFence.
// Without a token it is exactly runs.Save. When the repository cannot fence
// while a token is present, the save fails with runstate.ErrFenceRequired.
func (e *Engine) saveRunSnapshot(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	_, err := runstate.SaveWithFence(ctx, e.persist.runs, snapshot, expectedVersion)
	return err
}

// pauseWithRetry pauses through the human gate, retrying the whole
// load-then-pause sequence when a concurrent writer advances the run version
// between our load and the gate's own compare-and-swap save. Without this
// retry, a HumanGate.Pause implementation that uses a single fixed expected
// version turns a legitimate concurrent write into a hard run failure
// instead of a pause.
func (e *Engine) pauseWithRetry(ctx context.Context, runID string, build func(version int64) core.CheckpointState) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
		if err != nil {
			return "", err
		}
		token, err := e.coord.gate.Pause(ctx, build(snapshot.Version))
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return "", err
		}
		// Back off with jitter before reloading so a concurrent writer does
		// not keep winning the version race on every immediate retry.
		if delayErr := retryDelay(ctx, attempt); delayErr != nil {
			return "", delayErr
		}
	}
	return "", fmt.Errorf("runtime: failed to pause run %q after stale snapshot retries", runID)
}

func retryDelay(ctx context.Context, attempt int) error {
	return retry.Backoff(ctx, attempt)
}

func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (e *Engine) stepOutputRef(ctx context.Context, runID, key string, raw json.RawMessage) (runstate.StepOutputRef, error) {
	if e.gov.redactor != nil {
		redacted, err := e.gov.redactor.RedactOutput(ctx, governance.OutputRedaction{RunID: runID, StepID: key, Kind: "step_output", Data: raw})
		if err != nil {
			return runstate.StepOutputRef{}, err
		}
		raw = redacted
	}
	threshold := e.scenario.Runtime.StepOutputThreshold
	if threshold <= 0 || int64(len(raw)) <= threshold || e.persist.blobs == nil {
		return runstate.StepOutputRef{Inline: raw}, nil
	}
	ref, err := e.persist.blobs.Put(ctx, raw)
	if err != nil {
		return runstate.StepOutputRef{}, err
	}
	return runstate.StepOutputRef{Blob: &ref}, nil
}
