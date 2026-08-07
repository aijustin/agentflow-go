package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// streamDetachedKey marks caller-detached streaming (see WithStreamDetached
// on the framework facade): client disconnect must not cancel the run.
type streamDetachedKey struct{}

// defaultDetachedCancellationPollInterval is how often the detached-stream
// cancellation watcher reloads the run snapshot when the scenario does not
// override it (see core.RuntimePolicy.DetachedCancellationPollInterval).
// Polling is only the fallback for cancellations persisted by OTHER
// processes: a same-process settle (cancel/fail/complete/pause through the
// engine's helpers) wakes the watcher immediately via the run-status
// notifier, so the poll cadence no longer needs to be sub-second — 2s keeps
// cross-process cancellations responsive while cutting the steady-state
// repository read pressure per detached run by ~8x.
const defaultDetachedCancellationPollInterval = 2 * time.Second

// detachedCancellationPollInterval returns the configured detached-stream
// cancellation poll interval, falling back to the default when the scenario
// leaves it zero or negative.
func (e *Engine) detachedCancellationPollInterval() time.Duration {
	if interval := e.scenario.Runtime.DetachedCancellationPollInterval; interval > 0 {
		return interval
	}
	return defaultDetachedCancellationPollInterval
}

// ContextWithStreamDetached marks the stream to keep executing to a terminal
// state in the background when the caller's context is cancelled (e.g. client
// disconnect), instead of marking the run Cancelled.
func ContextWithStreamDetached(ctx context.Context) context.Context {
	return context.WithValue(ctx, streamDetachedKey{}, true)
}

func streamDetachedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(streamDetachedKey{}).(bool)
	return v
}

// StreamDetachedFromContext reports whether ctx carries the detached
// streaming marker (see ContextWithStreamDetached). The framework facade
// needs it to exempt detached streams from caller-gone fallbacks: a detached
// run explicitly keeps executing after the caller goes away.
func StreamDetachedFromContext(ctx context.Context) bool {
	return streamDetachedFromContext(ctx)
}

// chunkError returns the structured provider error a failed chunk carries
// when present, falling back to the wire-safe string form, so retry
// classification and persisted failure reasons keep the original error type.
func chunkError(chunk llm.ChatChunk) error {
	if chunk.Err != nil {
		return chunk.Err
	}
	return errors.New(chunk.Error)
}

func (e *Engine) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, err
	}
	// Reject before beginRun creates (and immediately has to fail) a run
	// snapshot for a request that can never succeed.
	if e.hasBeforeFinalCheckpoint(agent) {
		return nil, fmt.Errorf("runtime: streaming does not support before_final_answer checkpoint; use Run or RunStructured")
	}
	callerCtx := ctx
	detached := streamDetachedFromContext(ctx)
	// execCtx drives execution and terminal persistence. In detached mode it
	// is decoupled from the caller's context so a client disconnect does not
	// cancel the run; forwarding to the caller still observes callerCtx.
	execCtx := ctx
	if detached {
		execCtx = context.WithoutCancel(ctx)
	}
	var cancel context.CancelFunc
	if _, hasDeadline := execCtx.Deadline(); !hasDeadline {
		execCtx, cancel = e.withTimeout(execCtx, e.scenario.Runtime.Timeout)
	} else {
		cancel = func() {}
	}
	var cancelDetached context.CancelCauseFunc
	if detached {
		// A detached run ignores caller cancellation, but a lost run lease
		// must still abort it before another worker takes over the run.
		watchCtx, watchCancel := context.WithCancelCause(execCtx)
		cancelDetached = watchCancel
		execCtx = watchCtx
		defer func() {
			// Balances WithCancelCause for the synchronous part of Stream;
			// the goroutine below takes over cancellation afterwards.
			_ = watchCancel
		}()
		safecall.GoSafe("runtime: detached lease watcher", nil, func() {
			select {
			case <-callerCtx.Done():
				if cause := context.Cause(callerCtx); errors.Is(cause, coordination.ErrRunLeaseLost) {
					watchCancel(cause)
				}
			case <-watchCtx.Done():
			}
		})
	}
	execCtx = ContextWithTrustMode(execCtx, req.TrustMode)
	execCtx = core.ContextWithEpisodeCorrelation(execCtx, episodeCorrelationFromRequest(req))
	if err := e.beginRun(execCtx, &req); err != nil {
		cancel()
		return nil, err
	}
	execCtx = e.withEpisodeCorrelation(execCtx, req)
	if detached {
		observerCtx := execCtx
		safecall.GoSafe("runtime: detached cancellation watcher", nil, func() {
			// Fast path: a same-process settle of this run (cancel/fail/
			// complete/pause via the engine's helpers) broadcasts an
			// in-process hint that wakes the watcher immediately. Slow path:
			// the poll ticker remains the correctness fallback for
			// cancellations persisted by other processes, which never
			// produce a hint. Either way the watcher re-loads the snapshot
			// and confirms the status before acting — hints only cut
			// latency, they never decide.
			wake := make(chan struct{}, 1)
			unsubscribe := e.coord.statusNotifier.subscribe(req.RunID, wake)
			defer unsubscribe()
			ticker := time.NewTicker(e.detachedCancellationPollInterval())
			defer ticker.Stop()
			// check reports whether the watcher is done (run left Running,
			// or the load failed and the run was aborted).
			check := func() bool {
				snapshot, loadErr := runstate.LoadAuthorized(observerCtx, e.persist.runs, req.RunID)
				if loadErr != nil {
					cancelDetached(fmt.Errorf("runtime: observe detached run %q cancellation: %w", req.RunID, loadErr))
					return true
				}
				if snapshot.Status == runstate.RunStatusRunning {
					return false
				}
				if snapshot.Status == runstate.RunStatusCancelled {
					cancelDetached(ErrRunCancelled)
				}
				return true
			}
			for {
				select {
				case <-execCtx.Done():
					return
				case <-wake:
					if check() {
						return
					}
				case <-ticker.C:
					if check() {
						return
					}
				}
			}
		})
	}
	source, agent, streamCancel, err := e.streamAnswer(execCtx, req)
	if err != nil {
		e.markRunFailed(execCtx, req.RunID, err)
		cancel()
		return nil, err
	}
	out := make(chan llm.ChatChunk, 1)
	safecall.GoSafe("runtime: stream consumer", func(err error) {
		// The deferred cancel/streamCancel/close(out) above have already run.
		// Settle the run instead of leaving it Running forever: a crashed
		// consumer is a worker-side failure, so fail the run on a detached
		// persistence context (the consumer's own defers cancelled execCtx).
		e.markRunFailed(context.WithoutCancel(execCtx), req.RunID, err)
		e.logError(context.WithoutCancel(execCtx), "runtime: stream consumer crashed; run marked failed", "run_id", req.RunID, "error", err)
	}, func() {
		defer close(out)
		defer streamCancel()
		defer cancel()
		// forwarding reports whether the caller is still consuming. Once the
		// caller goes away, a detached run keeps executing (and settling the
		// run) without sending; a non-detached run applies the existing
		// cancelled/failed classification and stops.
		forwarding := true
		sentTerminal := false
		sendTerminal := func(c llm.ChatChunk) {
			if !forwarding || (sentTerminal && c.Error == "") {
				return
			}
			sentTerminal = true
			c.Done = true
			select {
			case out <- c:
			case <-callerCtx.Done():
			}
		}
		send := func(c llm.ChatChunk) bool {
			if !forwarding || sentTerminal {
				return false
			}
			if c.Done {
				sendTerminal(c)
				return true
			}
			select {
			case out <- c:
				return true
			case <-callerCtx.Done():
				return false
			}
		}
		// callerGone handles a forwarding failure. It reports whether the
		// consumer goroutine should stop entirely (non-detached) or keep
		// executing in the background (detached).
		callerGone := func() bool {
			if !detached {
				if err := callerCtx.Err(); errors.Is(err, context.DeadlineExceeded) {
					e.markRunFailed(execCtx, req.RunID, err)
				} else {
					e.markRunCancelled(execCtx, req.RunID)
				}
				return true
			}
			forwarding = false
			return false
		}
		var b strings.Builder
		toolsAgent := len(agent.Tools) > 0 || len(agent.SubAgents) > 0
		for chunk := range source {
			// Presentation answer deltas (non-terminal) feed the plain-chat
			// aggregate. Tool progress chunks must not. Done.Content is handled
			// below: tools path treats it as authoritative final; plain chat may
			// deliver content+Done in one chunk.
			if !chunk.Done && chunk.IsAnswerContent() && chunk.Content != "" {
				b.WriteString(chunk.Content)
			}
			if chunk.Done {
				finalOutput := b.String()
				if toolsAgent {
					if chunk.Content != "" {
						// answerWithTools returns terminal prose without tool-turn
						// preambles; prefer it over concatenated presentation tokens.
						finalOutput = chunk.Content
					}
					// When deltas were already streamed, strip Done.Content so
					// consumers do not receive a duplicate bulk prose frame.
					if b.Len() > 0 {
						chunk.Content = ""
					}
				} else if chunk.Content != "" && b.Len() == 0 {
					// Plain chat providers/mocks may send content only on Done.
					finalOutput = chunk.Content
				} else if b.Len() > 0 {
					// Content already forwarded via deltas; avoid re-emitting on Done.
					chunk.Content = ""
				}
				if !send(chunk) && callerGone() {
					return
				}
				if chunk.Paused {
					if err := e.ensureRunPaused(execCtx, req.RunID); err != nil {
						e.logWarn(execCtx, "runtime: failed to persist paused status after stream pause", "run_id", req.RunID, "error", err)
					}
					return
				}
				if chunk.Error != "" {
					e.markRunFailed(execCtx, req.RunID, chunkError(chunk))
					return
				}
				if err := e.completeStreamRun(execCtx, req.RunID, agent, req.Prompt, finalOutput); err != nil {
					// A completion that failed because the caller's context
					// died while the final Done chunk raced the send must
					// still settle the run; otherwise a cancelled-caller
					// stream would leave it Running forever.
					if ctxErr := execCtx.Err(); ctxErr != nil {
						e.markRunFailedOrCancelled(execCtx, req.RunID, ctxErr)
					}
					sendTerminal(llm.ChatChunk{Error: err.Error()})
				}
				return
			}
			if !send(chunk) {
				if callerGone() {
					return
				}
				continue
			}
			if chunk.Paused {
				if err := e.ensureRunPaused(execCtx, req.RunID); err != nil {
					e.logWarn(execCtx, "runtime: failed to persist paused status after stream pause", "run_id", req.RunID, "error", err)
				}
				return
			}
			if chunk.Error != "" {
				e.markRunFailed(execCtx, req.RunID, chunkError(chunk))
				return
			}
		}
		if err := execCtx.Err(); err != nil {
			if errors.Is(context.Cause(execCtx), ErrRunCancelled) {
				e.markRunCancelled(execCtx, req.RunID)
			} else if errors.Is(err, context.DeadlineExceeded) || detached {
				// Apart from explicit run cancellation, detached execution is
				// cancelled only by its own scenario timeout or a lost lease,
				// so those execution-context errors are genuine failures.
				e.markRunFailed(execCtx, req.RunID, err)
			} else {
				e.markRunCancelled(execCtx, req.RunID)
			}
			return
		}
		// The source closed without ever sending a Done chunk: the provider
		// stream was cut off mid-answer. Treating it as a normal completion
		// would persist a truncated output as the run's final answer.
		streamErr := errors.New("runtime: llm stream closed without a done chunk")
		e.markRunFailed(execCtx, req.RunID, streamErr)
		sendTerminal(llm.ChatChunk{Error: streamErr.Error()})
	})
	return out, nil
}
