package runtime

import (
	"context"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func statusNotifierSubscribers(engine *Engine, runID string) int {
	engine.coord.statusNotifier.mu.Lock()
	defer engine.coord.statusNotifier.mu.Unlock()
	return len(engine.coord.statusNotifier.subs[runID])
}

// waitForSubscriber blocks until the detached cancellation watcher has
// registered with the status notifier (it subscribes asynchronously in its
// own goroutine at Stream start).
func waitForSubscriber(t *testing.T, engine *Engine, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if statusNotifierSubscribers(engine, runID) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("detached watcher never subscribed for run %q", runID)
}

// DEFECT_REPORT D6: a same-process settle of a detached run must wake the
// cancellation watcher immediately via the in-process notifier — the poll
// interval (here 10s) is only the cross-process fallback, so a sub-second
// reaction proves the notification fast path.
func TestEngineDetachedWatcherWakesOnInProcessSettle(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := &blockingStreamGateway{started: make(chan struct{})}
	scenario := baseScenario(false)
	scenario.Runtime.DetachedCancellationPollInterval = 10 * time.Second
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(ContextWithStreamDetached(context.Background()),
		RunRequest{RunID: "run-detached-notify", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch {
		}
	}()
	select {
	case <-gateway.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	waitForSubscriber(t, engine, "run-detached-notify")

	start := time.Now()
	engine.MarkRunCancelled(context.Background(), "run-detached-notify")
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("detached watcher did not react to the in-process cancellation (poll interval is 10s)")
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("watcher reaction took %v; expected the notification path, not the 10s poll", elapsed)
	}
	loaded, err := repo.Load(context.Background(), "run-detached-notify")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled run, got %+v", loaded)
	}
	// The watcher must unsubscribe on exit: no leftover notifier entries.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if statusNotifierSubscribers(engine, "run-detached-notify") == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("status notifier still has a subscriber after the detached run ended")
}

// DEFECT_REPORT D6: a cancellation persisted by ANOTHER process produces no
// in-process notification; the poll ticker must still detect it.
func TestEngineDetachedWatcherPollFallbackDetectsExternalCancellation(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := &blockingStreamGateway{started: make(chan struct{})}
	scenario := baseScenario(false)
	scenario.Runtime.DetachedCancellationPollInterval = 50 * time.Millisecond
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(ContextWithStreamDetached(context.Background()),
		RunRequest{RunID: "run-detached-poll", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch {
		}
	}()
	select {
	case <-gateway.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}

	// Simulate a cross-process cancellation: write the terminal status
	// straight to the repository, bypassing the engine (and its notifier).
	snapshot, err := repo.Load(context.Background(), "run-detached-poll")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusCancelled
	if err := repo.Save(context.Background(), &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}

	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("poll fallback did not detect the externally persisted cancellation")
	}
	loaded, err := repo.Load(context.Background(), "run-detached-poll")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCancelled {
		t.Fatalf("cancelled snapshot was overwritten: status=%s", loaded.Status)
	}
}

func TestRunStatusNotifierSubscribeNotifyUnsubscribe(t *testing.T) {
	n := newRunStatusNotifier()
	wake := make(chan struct{}, 1)
	unsubscribe := n.subscribe("run-1", wake)

	n.notify("run-1")
	select {
	case <-wake:
	default:
		t.Fatal("expected wakeup after notify")
	}
	// A pending wakeup coalesces further hints (buffered channel, cap 1).
	n.notify("run-1")
	n.notify("run-1")
	<-wake
	select {
	case <-wake:
		t.Fatal("expected coalesced wakeups, got a second one")
	default:
	}

	unsubscribe()
	unsubscribe() // idempotent
	n.mu.Lock()
	remaining := len(n.subs)
	n.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("unsubscribe leaked notifier entries: %d", remaining)
	}
	// Notify with no subscribers must not block or panic.
	n.notify("run-1")
	select {
	case <-wake:
		t.Fatal("notify reached an unsubscribed channel")
	default:
	}
}
