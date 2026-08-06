package agentflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// StreamRunOption configures StreamRun read-side behavior.
type StreamRunOption func(*streamRunOptions)

type streamRunOptions struct {
	eventPreset core.EventFilterPreset
	detached    bool
}

// Event filter presets for StreamRun / EventStore.ListEvents read-side views.
const (
	EventFilterProductUI  = core.EventFilterProductUI
	EventFilterDiagnostic = core.EventFilterDiagnostic
)

// EventFilterPreset is a named read-side event view (product_ui | diagnostic).
type EventFilterPreset = core.EventFilterPreset

// WithStreamEventFilterPreset selects the event view for StreamFrameEvent frames.
// EventStore / WithEventSink still receive the full stream. Empty defaults to
// diagnostic (all events, including MemoryRead and ContextPrepared).
func WithStreamEventFilterPreset(preset EventFilterPreset) StreamRunOption {
	return func(opts *streamRunOptions) {
		opts.eventPreset = preset
	}
}

// WithStreamDetached detaches execution from the caller's context: when the
// caller's ctx is cancelled (e.g. the SSE client disconnects), the run is NOT
// marked Cancelled but keeps executing in the background until it reaches a
// terminal state and persists its result normally. The frame channel closes
// when the caller goes away; the terminal state is observable afterwards via
// the run-state repository. An explicit Framework-level cancellation of the
// run (Cancel API) and a lost run lease still abort it.
func WithStreamDetached() StreamRunOption {
	return func(opts *streamRunOptions) {
		opts.detached = true
	}
}

// StreamDetached marks ctx so a run started by Framework.Stream keeps
// executing to a terminal state in the background when the caller's context
// is cancelled (client disconnect), instead of being marked Cancelled. It is
// the context-level equivalent of the WithStreamDetached StreamRun option for
// callers of the lower-level Stream API. An explicit run Cancel and a lost
// run lease still abort the run.
func StreamDetached(ctx context.Context) context.Context {
	return runtime.ContextWithStreamDetached(ctx)
}

// StreamFrameKind discriminates unified StreamRun frames.
type StreamFrameKind string

const (
	StreamFrameToken StreamFrameKind = "token"
	StreamFrameEvent StreamFrameKind = "event"
	StreamFrameDone  StreamFrameKind = "done"
	StreamFrameError StreamFrameKind = "error"
	// StreamFrameEventsLost marks that teed events were dropped because the
	// frame consumer fell behind; EventsLost carries the cumulative count.
	// Token frames are never dropped, so the stream stays authoritative for
	// the answer itself.
	StreamFrameEventsLost StreamFrameKind = "events_lost"
)

// StreamFrame is a unified token/event/terminal frame for StreamRun.
type StreamFrame struct {
	Kind   StreamFrameKind
	Chunk  *llm.ChatChunk // for token
	Event  *core.Event    // for event
	Err    error
	Result *RunResult // for done
	// EventsLost carries the cumulative number of teed events dropped so far
	// when Kind is StreamFrameEventsLost.
	EventsLost int64
}

type streamEventTee struct {
	runID   string
	events  chan core.Event
	done    atomic.Bool
	dropped atomic.Int64
}

func (t *streamEventTee) Emit(_ context.Context, event core.Event) error {
	if t.done.Load() {
		return nil
	}
	if event.RunID != "" && event.RunID != t.runID {
		return nil
	}
	select {
	case t.events <- event:
	default:
		// Drop when the consumer is behind; tokens remain authoritative.
		// Count every drop so StreamRun can surface an events_lost marker
		// frame instead of losing events silently.
		t.dropped.Add(1)
	}
	return nil
}

// StreamRun executes a run and merges LLM token chunks with runtime events
// into a single frame channel. Existing Stream remains unchanged.
//
// Events are teed via a context-scoped sink for the duration of the stream so
// callers do not need an EventHub. Done is emitted only after the token stream
// closes and pending teed events have been flushed into the frame channel.
//
// Use WithStreamEventFilterPreset to project events for product_ui vs diagnostic
// views. Default is diagnostic (full stream).
func (f *Framework) StreamRun(ctx context.Context, req RunRequest, opts ...StreamRunOption) (<-chan StreamFrame, error) {
	options := streamRunOptions{eventPreset: core.EventFilterDiagnostic}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	options.eventPreset = core.NormalizeEventFilterPreset(options.eventPreset)

	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	tee := &streamEventTee{
		runID:  req.RunID,
		events: make(chan core.Event, 64),
	}
	ctx = runtime.ContextWithEventTee(ctx, tee)
	if options.detached {
		ctx = runtime.ContextWithStreamDetached(ctx)
	}

	// Register the run's hub session up front so AttachRunStream subscribers
	// can follow from the first frame (and across a HITL pause/resume). The
	// primary out channel keeps its historic blocking delivery; the hub only
	// observes frames after they are sent there.
	session := f.streamHub.register(req.RunID)
	chunks, err := f.Stream(ctx, req)
	if err != nil {
		f.streamHub.unregister(req.RunID, session)
		return nil, err
	}

	out := make(chan StreamFrame)
	safecall.GoSafe("agentflow: stream frame merger", func(err error) {
		// The deferred close(out) above has already run, so consumers see a
		// closed channel; the run itself is unaffected (the engine and the
		// lease forwarder run on their own goroutines).
		if f.logger != nil {
			f.logger.Error(context.WithoutCancel(ctx), "agentflow: stream frame merger crashed", "run_id", req.RunID, "error", err)
		}
	}, func() {
		defer close(out)
		var (
			streamErr error
			paused    bool
			pauseTok  string
			output    strings.Builder
			reported  int64
		)
		send := func(frame StreamFrame) bool {
			select {
			case out <- frame:
				// Fan the frame out to hub subscribers only after the primary
				// consumer received it, so attach/replay can never starve or
				// reorder the historic stream.
				f.streamHub.publish(req.RunID, frame)
				return true
			case <-ctx.Done():
				return false
			}
		}
		flushEvents := func() {
			for {
				select {
				case event := <-tee.events:
					if !options.eventPreset.Allows(event.Type) {
						continue
					}
					cloned := event
					if !send(StreamFrame{Kind: StreamFrameEvent, Event: &cloned}) {
						return
					}
				default:
					// Surface dropped teed events as a marker frame (with the
					// cumulative count) instead of losing them silently.
					if n := tee.dropped.Load(); n > reported {
						reported = n
						send(StreamFrame{Kind: StreamFrameEventsLost, EventsLost: n})
					}
					return
				}
			}
		}

		for chunk := range chunks {
			chunkCopy := chunk
			if chunk.IsAnswerContent() && chunk.Content != "" {
				output.WriteString(chunk.Content)
			}
			if chunk.Error != "" {
				streamErr = streamFrameError(chunk.Error)
			}
			if chunk.Paused {
				paused = true
				pauseTok = chunk.PauseToken
			}
			if !send(StreamFrame{Kind: StreamFrameToken, Chunk: &chunkCopy}) {
				for range chunks {
				}
				tee.done.Store(true)
				return
			}
			flushEvents()
		}
		tee.done.Store(true)
		flushEvents()

		if streamErr != nil {
			send(StreamFrame{Kind: StreamFrameError, Err: streamErr})
			return
		}

		result := &RunResult{RunID: req.RunID, Output: output.String()}
		if paused {
			result.Status = runstate.RunStatusPaused
			result.Token = pauseTok
		} else {
			snapshot, loadErr := runstate.LoadAuthorized(ctx, f.runs, req.RunID)
			if loadErr != nil {
				if f.logger != nil {
					f.logger.Warn(ctx, "agentflow: StreamRun failed to load run snapshot for done frame", "run_id", req.RunID, "error", loadErr)
				}
				send(StreamFrame{Kind: StreamFrameError, Err: fmt.Errorf("agentflow: load run for stream done: %w", loadErr)})
				return
			}
			result.Status = snapshot.Status
			if final, ok := snapshot.StepOutputs["final"]; ok {
				raw, finalErr := runstate.LoadStepOutput(ctx, f.blobs, final)
				if finalErr != nil {
					if f.logger != nil {
						f.logger.Warn(ctx, "agentflow: StreamRun failed to load final output", "run_id", req.RunID, "error", finalErr)
					}
					send(StreamFrame{Kind: StreamFrameError, Err: fmt.Errorf("agentflow: load final for stream done: %w", finalErr)})
					return
				}
				result.Output = string(raw)
				result.StructuredOutput = append(json.RawMessage(nil), raw...)
			}
		}
		send(StreamFrame{Kind: StreamFrameDone, Result: result})
	})
	return out, nil
}

type streamFrameError string

func (e streamFrameError) Error() string { return string(e) }

// teeEventSink fans every emitted event out to the context-scoped StreamRun
// tee (when one is attached) in addition to the configured sink. The workflow
// runner emits straight into a sink — unlike the engine and the facade, which
// consult the tee in their own emit helpers — so wrapping the sink handed to
// it is what carries workflow/hybrid node events into the StreamRun frame
// stream.
type teeEventSink struct {
	inner core.EventSink
}

func (s teeEventSink) Emit(ctx context.Context, event core.Event) error {
	err := s.inner.Emit(ctx, event)
	if tee := runtime.EventTeeFromContext(ctx); tee != nil {
		if teeErr := tee.Emit(ctx, event); teeErr != nil && err == nil {
			err = teeErr
		}
	}
	return err
}
