package agentflow

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// StreamFrameKind discriminates unified StreamRun frames.
type StreamFrameKind string

const (
	StreamFrameToken StreamFrameKind = "token"
	StreamFrameEvent StreamFrameKind = "event"
	StreamFrameDone  StreamFrameKind = "done"
	StreamFrameError StreamFrameKind = "error"
)

// StreamFrame is a unified token/event/terminal frame for StreamRun.
type StreamFrame struct {
	Kind   StreamFrameKind
	Chunk  *llm.ChatChunk // for token
	Event  *core.Event    // for event
	Err    error
	Result *RunResult // for done
}

type streamEventTee struct {
	runID  string
	events chan core.Event
	done   atomic.Bool
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
	}
	return nil
}

// StreamRun executes a run and merges LLM token chunks with runtime events
// into a single frame channel. Existing Stream remains unchanged.
//
// Events are teed via a context-scoped sink for the duration of the stream so
// callers do not need an EventHub. Done is emitted only after the token stream
// closes and pending teed events have been flushed into the frame channel.
func (f *Framework) StreamRun(ctx context.Context, req RunRequest) (<-chan StreamFrame, error) {
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	tee := &streamEventTee{
		runID:  req.RunID,
		events: make(chan core.Event, 64),
	}
	ctx = runtime.ContextWithEventTee(ctx, tee)

	chunks, err := f.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamFrame)
	go func() {
		defer close(out)
		var (
			streamErr error
			paused    bool
			pauseTok  string
			output    string
		)
		send := func(frame StreamFrame) bool {
			select {
			case out <- frame:
				return true
			case <-ctx.Done():
				return false
			}
		}
		flushEvents := func() {
			for {
				select {
				case event := <-tee.events:
					cloned := event
					if !send(StreamFrame{Kind: StreamFrameEvent, Event: &cloned}) {
						return
					}
				default:
					return
				}
			}
		}

		for chunk := range chunks {
			chunkCopy := chunk
			if chunk.IsAnswerContent() && chunk.Content != "" {
				output += chunk.Content
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

		result := &RunResult{RunID: req.RunID, Output: output}
		if paused {
			result.Status = runstate.RunStatusPaused
			result.Token = pauseTok
		} else if snapshot, loadErr := f.runs.Load(ctx, req.RunID); loadErr == nil {
			result.Status = snapshot.Status
			if final, ok := snapshot.StepOutputs["final"]; ok && len(final.Inline) > 0 {
				result.Output = string(final.Inline)
				result.StructuredOutput = append(json.RawMessage(nil), final.Inline...)
			}
		} else {
			result.Status = runstate.RunStatusCompleted
		}
		send(StreamFrame{Kind: StreamFrameDone, Result: result})
	}()
	return out, nil
}

type streamFrameError string

func (e streamFrameError) Error() string { return string(e) }
