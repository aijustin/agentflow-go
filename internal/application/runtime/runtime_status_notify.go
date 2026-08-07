package runtime

import "sync"

// runStatusNotifier is an in-process hint bus for run-status transitions.
// When this process moves a run out of Running (terminal settle, pause), it
// broadcasts a wakeup so interested parties — currently the detached-stream
// cancellation watcher — can re-check the snapshot immediately instead of
// waiting for the next poll tick.
//
// A notification is only a hint: it can race the actual persistence, arrive
// for a state the subscriber does not care about, or never arrive at all when
// the transition was written by another process. Subscribers must re-load the
// snapshot to confirm the state; polling remains the correctness mechanism,
// notifications only cut the latency of the in-process fast path.
type runStatusNotifier struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{} // runID -> subscriber wake channels
}

func newRunStatusNotifier() *runStatusNotifier {
	return &runStatusNotifier{subs: make(map[string]map[chan struct{}]struct{})}
}

// subscribe registers wake (must be buffered, capacity >= 1) for runID and
// returns an idempotent unsubscribe function. Callers must eventually invoke
// it; unsubscribing the last subscriber of a run drops the run's entry so a
// long-lived engine does not accumulate stale keys.
func (n *runStatusNotifier) subscribe(runID string, wake chan struct{}) func() {
	n.mu.Lock()
	defer n.mu.Unlock()
	set := n.subs[runID]
	if set == nil {
		set = make(map[chan struct{}]struct{})
		n.subs[runID] = set
	}
	set[wake] = struct{}{}
	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if set := n.subs[runID]; set != nil {
			delete(set, wake)
			if len(set) == 0 {
				delete(n.subs, runID)
			}
		}
	}
}

// notify wakes every subscriber of runID. A full wake channel means a wakeup
// is already pending (the subscriber has not consumed the previous hint yet),
// so the send is skipped — coalescing repeated hints is exactly what the
// buffered channel is for.
func (n *runStatusNotifier) notify(runID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for wake := range n.subs[runID] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// notifyRunStatusSettled broadcasts an in-process hint that runID may have
// left the Running state. It is called by the engine's terminal/pause settle
// helpers after their persistence attempt (success or not: a competing
// in-process writer that won the CAS race may have settled the run too).
func (e *Engine) notifyRunStatusSettled(runID string) {
	if e == nil || e.coord.statusNotifier == nil || runID == "" {
		return
	}
	e.coord.statusNotifier.notify(runID)
}
