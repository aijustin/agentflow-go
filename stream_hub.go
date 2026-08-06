package agentflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	// streamHubRingCapacity bounds the per-run replay buffer kept for
	// late-attaching subscribers. Overflow evicts the oldest frames and is
	// surfaced to attachers as an events_lost gap marker.
	streamHubRingCapacity = 1024
	// streamSubscriberBuffer is the per-subscriber channel capacity. A
	// subscriber that falls behind loses event frames (counted, surfaced via
	// events_lost markers); token/done/error frames are queued in an
	// overflow backlog and are never dropped, mirroring the StreamRun tee
	// contract that the answer stream stays authoritative.
	streamSubscriberBuffer = 256
)

// streamSessionGracePeriod keeps a terminated run's session attachable for
// late subscribers before the hub reclaims it. It is a var so tests can
// shorten the grace period.
var streamSessionGracePeriod = 30 * time.Second

// errStreamRunNotActive is returned by streamHub.attach when the run has no
// live session; AttachRunStream then falls back to EventStore replay.
var errStreamRunNotActive = errors.New("agentflow: run has no active stream session")

// streamHub fans StreamRun frames out to any number of attached subscribers
// and keeps a bounded replay ring per run. The primary StreamRun channel is
// not a hub subscriber: it keeps its historic blocking, never-dropped
// delivery; the hub only observes published frames.
type streamHub struct {
	mu       sync.Mutex
	sessions map[string]*streamSession
	closed   bool
}

type streamSession struct {
	runID string
	// ring is a circular replay buffer of the last streamHubRingCapacity
	// published frames; ringHead indexes the oldest frame.
	ring        []StreamFrame
	ringHead    int
	ringLen     int
	ringEvicted int64
	subs        map[int]*streamSubscriber
	nextSubID   int
	terminal    bool
	graceTimer  *time.Timer
}

type streamSubscriber struct {
	id     int
	ch     chan StreamFrame
	preset core.EventFilterPreset
	done   chan struct{}
	// finished is closed when the subscriber is closed (terminal delivery,
	// detach, hub close) so the ctx-watch goroutine in AttachRunStream can
	// exit even when the caller's context is never cancelled.
	finished chan struct{}
	// backlog holds reliable frames (token/done/error) that did not fit ch,
	// drained FIFO by a lazily started drainer goroutine.
	backlog []StreamFrame
	// lost counts droppable frames (event / events_lost) dropped because the
	// subscriber fell behind; lostSent tracks the last cumulative count
	// surfaced through an events_lost marker.
	lost     int64
	lostSent int64
	draining bool
	detached bool
	closed   bool
}

func newStreamHub() *streamHub {
	return &streamHub{sessions: make(map[string]*streamSession)}
}

func streamFrameDroppable(frame StreamFrame) bool {
	return frame.Kind == StreamFrameEvent || frame.Kind == StreamFrameEventsLost
}

// streamFrameTerminal reports whether frame ends the stream. A paused Done is
// deliberately not terminal: approval waits are a first-class stream state,
// and ResumeAndContinue keeps publishing into the same session afterwards.
func streamFrameTerminal(frame StreamFrame) bool {
	switch frame.Kind {
	case StreamFrameError:
		return true
	case StreamFrameDone:
		return frame.Result == nil || frame.Result.Status != runstate.RunStatusPaused
	default:
		return false
	}
}

func (s *streamSession) ringAppend(frame StreamFrame) {
	if s.ring == nil {
		s.ring = make([]StreamFrame, streamHubRingCapacity)
	}
	if s.ringLen < streamHubRingCapacity {
		s.ring[(s.ringHead+s.ringLen)%streamHubRingCapacity] = frame
		s.ringLen++
		return
	}
	s.ring[s.ringHead] = frame
	s.ringHead = (s.ringHead + 1) % streamHubRingCapacity
	s.ringEvicted++
}

func (s *streamSession) ringFrames() []StreamFrame {
	out := make([]StreamFrame, 0, s.ringLen)
	for i := 0; i < s.ringLen; i++ {
		out = append(out, s.ring[(s.ringHead+i)%streamHubRingCapacity])
	}
	return out
}

// register starts a fresh session for runID, replacing any previous one
// (its subscribers are detached). Returns nil once the hub is closed.
func (h *streamHub) register(runID string) *streamSession {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	if old := h.sessions[runID]; old != nil {
		if old.graceTimer != nil {
			old.graceTimer.Stop()
		}
		for _, sub := range old.subs {
			h.detachLocked(old, sub)
		}
	}
	session := &streamSession{runID: runID, subs: make(map[int]*streamSubscriber)}
	h.sessions[runID] = session
	return session
}

// unregister removes session if it is still the live one for runID.
func (h *streamHub) unregister(runID string, session *streamSession) {
	if h == nil || session == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[runID] != session {
		return
	}
	if session.graceTimer != nil {
		session.graceTimer.Stop()
	}
	for _, sub := range session.subs {
		h.detachLocked(session, sub)
	}
	delete(h.sessions, runID)
}

// publish appends frame to the session ring and fans it out to all
// subscribers. It never blocks: subscriber channels are bounded, droppable
// frames are counted as lost when a subscriber is behind, and reliable
// frames go to the subscriber's overflow backlog drained by its own
// goroutine. All channel sends here are non-blocking and happen under the
// hub lock, so publishers (the StreamRun merger, resume paths) never stall
// on a slow subscriber.
func (h *streamHub) publish(runID string, frame StreamFrame) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	session := h.sessions[runID]
	if session == nil {
		return
	}
	session.ringAppend(frame)
	for _, sub := range session.subs {
		h.deliverLocked(session, sub, frame)
	}
	if streamFrameTerminal(frame) && !session.terminal {
		session.terminal = true
		session.graceTimer = time.AfterFunc(streamSessionGracePeriod, func() {
			h.unregister(runID, session)
		})
	}
}

func (h *streamHub) deliverLocked(session *streamSession, sub *streamSubscriber, frame StreamFrame) {
	if sub.closed || sub.detached {
		return
	}
	// Preset filtering is a read-side projection, not loss.
	if frame.Kind == StreamFrameEvent && frame.Event != nil && !sub.preset.Allows(frame.Event.Type) {
		return
	}
	switch {
	case len(sub.backlog) > 0 || sub.draining:
		// A drainer owns the channel while the backlog is non-empty; queue to
		// preserve ordering.
		if streamFrameDroppable(frame) {
			sub.lost++
		} else {
			sub.backlog = append(sub.backlog, frame)
		}
	default:
		select {
		case sub.ch <- frame:
		default:
			if streamFrameDroppable(frame) {
				sub.lost++
			} else {
				sub.backlog = append(sub.backlog, frame)
				sub.draining = true
				go h.drainSubscriber(session, sub)
			}
		}
	}
	if streamFrameTerminal(frame) {
		h.finishSubscriberLocked(session, sub)
	}
}

// finishSubscriberLocked closes a subscriber after its terminal frame. When
// a drainer is active it closes the channel once the backlog (which includes
// the terminal frame) is flushed.
func (h *streamHub) finishSubscriberLocked(session *streamSession, sub *streamSubscriber) {
	if sub.closed || sub.detached || len(sub.backlog) > 0 || sub.draining {
		return
	}
	// Surface any pending loss count before closing, best effort.
	if sub.lost > sub.lostSent {
		select {
		case sub.ch <- StreamFrame{Kind: StreamFrameEventsLost, EventsLost: sub.lost}:
			sub.lostSent = sub.lost
		default:
		}
	}
	sub.closed = true
	close(sub.finished)
	delete(session.subs, sub.id)
	close(sub.ch)
}

// drainSubscriber flushes a subscriber's backlog FIFO. It is the only
// goroutine that blocks on a subscriber channel, and it aborts via done when
// the subscriber is detached or the hub closes. When the backlog is empty it
// flushes a pending events_lost marker and, for terminated sessions or
// detached subscribers, closes the channel.
func (h *streamHub) drainSubscriber(session *streamSession, sub *streamSubscriber) {
	for {
		h.mu.Lock()
		if len(sub.backlog) > 0 && !sub.detached {
			frame := sub.backlog[0]
			h.mu.Unlock()
			select {
			case sub.ch <- frame:
				h.mu.Lock()
				sub.backlog = sub.backlog[1:]
				h.mu.Unlock()
			case <-sub.done:
			}
			continue
		}
		if sub.detached {
			sub.backlog = nil
			sub.draining = false
			if !sub.closed {
				sub.closed = true
				close(sub.finished)
				delete(session.subs, sub.id)
				h.mu.Unlock()
				close(sub.ch)
			} else {
				h.mu.Unlock()
			}
			return
		}
		if sub.lost > sub.lostSent {
			marker := StreamFrame{Kind: StreamFrameEventsLost, EventsLost: sub.lost}
			sub.lostSent = sub.lost
			h.mu.Unlock()
			select {
			case sub.ch <- marker:
			case <-sub.done:
			}
			continue
		}
		sub.draining = false
		shouldClose := !sub.closed && (session.terminal || h.closed)
		if shouldClose {
			sub.closed = true
			close(sub.finished)
			delete(session.subs, sub.id)
		}
		h.mu.Unlock()
		if shouldClose {
			close(sub.ch)
		}
		return
	}
}

// attach registers a subscriber and seeds its backlog with the ring replay
// (preset-filtered), prefixed by an events_lost gap marker when ring
// overflow evicted frames. Replay is queued through the backlog so live
// frames published after attach can never overtake it.
func (h *streamHub) attach(runID string, preset core.EventFilterPreset) (*streamSubscriber, error) {
	if h == nil {
		return nil, errStreamRunNotActive
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("agentflow: stream hub is closed")
	}
	session := h.sessions[runID]
	if session == nil {
		return nil, errStreamRunNotActive
	}
	sub := &streamSubscriber{
		id:       session.nextSubID,
		ch:       make(chan StreamFrame, streamSubscriberBuffer),
		preset:   preset,
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	session.nextSubID++
	if session.ringEvicted > 0 {
		sub.backlog = append(sub.backlog, StreamFrame{Kind: StreamFrameEventsLost, EventsLost: session.ringEvicted})
	}
	for _, frame := range session.ringFrames() {
		if frame.Kind == StreamFrameEvent && frame.Event != nil && !preset.Allows(frame.Event.Type) {
			continue
		}
		sub.backlog = append(sub.backlog, frame)
	}
	session.subs[sub.id] = sub
	if len(sub.backlog) > 0 || session.terminal {
		sub.draining = true
		go h.drainSubscriber(session, sub)
	}
	return sub, nil
}

func (h *streamHub) detach(runID string, subID int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[runID]
	if session == nil {
		return
	}
	if sub := session.subs[subID]; sub != nil {
		h.detachLocked(session, sub)
	}
}

func (h *streamHub) detachLocked(session *streamSession, sub *streamSubscriber) {
	if sub.closed || sub.detached {
		return
	}
	sub.detached = true
	close(sub.done)
	if !sub.draining {
		sub.closed = true
		close(sub.finished)
		delete(session.subs, sub.id)
		close(sub.ch)
	}
}

// sessionActive reports whether runID has a live, non-terminal session that
// resume/continue paths may keep publishing into.
func (h *streamHub) sessionActive(runID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[runID]
	return session != nil && !session.terminal
}

func (h *streamHub) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for runID, session := range h.sessions {
		if session.graceTimer != nil {
			session.graceTimer.Stop()
		}
		for _, sub := range session.subs {
			h.detachLocked(session, sub)
		}
		delete(h.sessions, runID)
	}
}

// streamHubTee is the core.EventSink attached to resume/continue contexts so
// events emitted after a HITL resume keep flowing into the run's stream
// session while the original StreamRun tee is long gone.
type streamHubTee struct {
	hub   *streamHub
	runID string
}

func (t streamHubTee) Emit(_ context.Context, event core.Event) error {
	if event.RunID != "" && event.RunID != t.runID {
		return nil
	}
	cloned := event
	t.hub.publish(t.runID, StreamFrame{Kind: StreamFrameEvent, Event: &cloned})
	return nil
}

// AttachRunStream subscribes to the unified frame stream of a run that is
// (or was recently) driven by StreamRun on this Framework. The subscriber
// first receives a replay of the session's ring buffer (prefixed by an
// events_lost gap marker when ring overflow evicted frames) and then live
// frames as they are published. A paused run is not a stream end: after
// ResumeAndContinue, execution keeps publishing into the same session.
//
// Subscriber channels are bounded (streamSubscriberBuffer). A subscriber
// that falls behind loses event frames — surfaced as events_lost marker
// frames with the cumulative count — while token and terminal frames are
// never dropped. Cancelling ctx detaches the subscriber and closes its
// channel; a terminal frame also closes the channel after delivery, and
// terminated sessions stay attachable for a 30s grace period.
//
// When the run has no live session (process restart, grace expired), the
// stream is reassembled from the configured EventStore (WithEventStore)
// followed by a synthetic Done frame from the persisted run state; without
// an event store an error is returned.
func (f *Framework) AttachRunStream(ctx context.Context, runID string, opts ...StreamRunOption) (<-chan StreamFrame, error) {
	options := streamRunOptions{eventPreset: core.EventFilterDiagnostic}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	options.eventPreset = core.NormalizeEventFilterPreset(options.eventPreset)

	if sub, err := f.streamHub.attach(runID, options.eventPreset); err == nil {
		go func() {
			select {
			case <-ctx.Done():
				f.streamHub.detach(runID, sub.id)
			case <-sub.finished:
			}
		}()
		return sub.ch, nil
	} else if !errors.Is(err, errStreamRunNotActive) {
		return nil, err
	}
	return f.replayRunStreamFromStore(ctx, runID, options.eventPreset)
}

// replayRunStreamFromStore reassembles a frame stream for a run with no live
// hub session from the durable event store, appending a synthetic Done frame
// from the persisted run snapshot.
func (f *Framework) replayRunStreamFromStore(ctx context.Context, runID string, preset core.EventFilterPreset) (<-chan StreamFrame, error) {
	if f.eventStore == nil {
		return nil, fmt.Errorf("agentflow: run %q has no active stream session and no event store is configured for replay: %w", runID, errStreamRunNotActive)
	}
	out := make(chan StreamFrame, streamSubscriberBuffer)
	safecall.GoSafe("agentflow: stream store replay", func(err error) {
		if f.logger != nil {
			f.logger.Error(context.WithoutCancel(ctx), "agentflow: stream store replay crashed", "run_id", runID, "error", err)
		}
	}, func() {
		defer close(out)
		send := func(frame StreamFrame) bool {
			select {
			case out <- frame:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var afterSequence int64
		for {
			records, err := f.eventStore.ListEvents(ctx, runID, observability.EventQuery{
				AfterSequence: afterSequence,
				Limit:         observability.MaxEventQueryLimit,
				Preset:        preset,
			})
			if err != nil {
				send(StreamFrame{Kind: StreamFrameError, Err: fmt.Errorf("agentflow: replay run %q events: %w", runID, err)})
				return
			}
			for _, record := range records {
				event := record.Event
				if !send(StreamFrame{Kind: StreamFrameEvent, Event: &event}) {
					return
				}
				if record.Sequence > afterSequence {
					afterSequence = record.Sequence
				}
			}
			if len(records) < observability.MaxEventQueryLimit {
				break
			}
		}
		snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
		if err != nil {
			return
		}
		result := &RunResult{RunID: runID, Status: snapshot.Status}
		if final, ok := snapshot.StepOutputs["final"]; ok {
			if raw, loadErr := runstate.LoadStepOutput(ctx, f.blobs, final); loadErr == nil {
				result.Output = string(raw)
			}
		}
		send(StreamFrame{Kind: StreamFrameDone, Result: result})
	})
	return out, nil
}
