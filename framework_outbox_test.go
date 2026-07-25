package agentflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	observabilityinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// fakeOutboxRepo is an in-memory runstate.Repository that also implements
// runstate.OutboxRepository, so framework relay tests do not need a database.
type fakeOutboxRepo struct {
	runstate.Repository

	mu          sync.Mutex
	rows        []runstate.OutboxEvent
	publishedAt map[int64]time.Time
	nextID      int64
}

func newFakeOutboxRepo() *fakeOutboxRepo {
	return &fakeOutboxRepo{
		Repository:  runstateinmem.NewRepository(),
		publishedAt: make(map[int64]time.Time),
	}
}

func (r *fakeOutboxRepo) park(sequence int64, event core.Event) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := r.nextID
	r.rows = append(r.rows, runstate.OutboxEvent{ID: id, Sequence: sequence, Event: event, CreatedAt: time.Now().UTC()})
	return id
}

func (r *fakeOutboxRepo) FetchUnpublishedOutbox(ctx context.Context, limit int) ([]runstate.OutboxEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]runstate.OutboxEvent, 0)
	for _, row := range r.rows {
		if _, published := r.publishedAt[row.ID]; published {
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeOutboxRepo) MarkOutboxPublished(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, published := r.publishedAt[id]; !published {
		r.publishedAt[id] = time.Now().UTC()
	}
	return nil
}

func (r *fakeOutboxRepo) DeleteOutboxForRun(ctx context.Context, runID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.rows[:0]
	var removed int64
	for _, row := range r.rows {
		if row.Event.RunID == runID {
			delete(r.publishedAt, row.ID)
			removed++
			continue
		}
		kept = append(kept, row)
	}
	r.rows = kept
	return removed, nil
}

func (r *fakeOutboxRepo) PurgeOutboxPublishedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.rows[:0]
	var removed int64
	for _, row := range r.rows {
		publishedAt, published := r.publishedAt[row.ID]
		if published && publishedAt.Before(cutoff) {
			delete(r.publishedAt, row.ID)
			removed++
			continue
		}
		kept = append(kept, row)
	}
	r.rows = kept
	return removed, nil
}

func (r *fakeOutboxRepo) unpublishedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, row := range r.rows {
		if _, published := r.publishedAt[row.ID]; !published {
			count++
		}
	}
	return count
}

func (r *fakeOutboxRepo) rowCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

// fakeSequencedStore wraps the in-memory event store with failure injection
// and delivery counting for relay tests.
type fakeSequencedStore struct {
	*observabilityinmem.Store

	mu         sync.Mutex
	failing    bool
	deliveries int
}

func (s *fakeSequencedStore) setFailing(failing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing = failing
}

func (s *fakeSequencedStore) AppendSequenced(ctx context.Context, sequence int64, event core.Event) (obspkg.EventRecord, error) {
	s.mu.Lock()
	if s.failing {
		s.mu.Unlock()
		return obspkg.EventRecord{}, fmt.Errorf("injected store outage")
	}
	s.deliveries++
	s.mu.Unlock()
	return s.Store.AppendSequenced(ctx, sequence, event)
}

// plainEventStore implements observability.EventStore but none of the
// extension interfaces, for option-validation tests.
type plainEventStore struct{}

func (plainEventStore) Append(context.Context, core.Event) (obspkg.EventRecord, error) {
	return obspkg.EventRecord{}, nil
}
func (plainEventStore) ListRuns(context.Context, obspkg.RunQuery) ([]obspkg.RunSummary, error) {
	return nil, nil
}
func (plainEventStore) ListEvents(context.Context, string, obspkg.EventQuery) ([]obspkg.EventRecord, error) {
	return nil, nil
}
func (plainEventStore) ListScopedEvents(context.Context, obspkg.ScopedEventQuery) ([]obspkg.EventRecord, error) {
	return nil, nil
}

func outboxTestScenario() core.Scenario {
	return core.Scenario{
		Name:   "outbox-test",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}
}

func TestWithOutboxRelayRequiresSequencedEventStore(t *testing.T) {
	_, err := New(outboxTestScenario(),
		WithRunStateRepository(newFakeOutboxRepo()),
		WithEventStore(plainEventStore{}),
		WithOutboxRelay(time.Millisecond),
	)
	if err == nil {
		t.Fatal("expected error for event store without SequencedEventStore")
	}
}

func TestWithOutboxRelayRequiresEventStore(t *testing.T) {
	_, err := New(outboxTestScenario(),
		WithRunStateRepository(newFakeOutboxRepo()),
		WithOutboxRelay(time.Millisecond),
	)
	if err == nil {
		t.Fatal("expected error when no event store is wired")
	}
}

func TestWithOutboxRelayRequiresOutboxCapableRepository(t *testing.T) {
	_, err := New(outboxTestScenario(),
		WithRunStateRepository(runstateinmem.NewRepository()),
		WithEventStore(observabilityinmem.NewStore()),
		WithOutboxRelay(time.Millisecond),
	)
	if err == nil {
		t.Fatal("expected error for runstate repository without outbox support")
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestOutboxRelayDeliversAndMarksPublished(t *testing.T) {
	repo := newFakeOutboxRepo()
	store := &fakeSequencedStore{Store: observabilityinmem.NewStore()}
	repo.park(1, core.Event{Type: core.EventRunStarted, RunID: "run-1", Timestamp: time.Now().UTC()})
	repo.park(2, core.Event{Type: core.EventRunCompleted, RunID: "run-1", Timestamp: time.Now().UTC()})
	fw, err := New(outboxTestScenario(),
		WithRunStateRepository(repo),
		WithEventStore(store),
		WithOutboxRelay(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fw.Close(context.Background()) }()
	waitForCondition(t, 5*time.Second, "relay delivery", func() bool {
		return repo.unpublishedCount() == 0
	})
	events, err := store.ListEvents(context.Background(), "run-1", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 delivered events, got %+v", events)
	}
	if events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("events must keep their minted sequences, got %+v", events)
	}
}

// A failing store leaves rows unpublished; once it recovers the relay
// redelivers without duplicating.
func TestOutboxRelayRetriesAfterStoreOutage(t *testing.T) {
	repo := newFakeOutboxRepo()
	store := &fakeSequencedStore{Store: observabilityinmem.NewStore(), failing: true}
	repo.park(1, core.Event{Type: core.EventRunStarted, RunID: "run-1", Timestamp: time.Now().UTC()})
	fw, err := New(outboxTestScenario(),
		WithRunStateRepository(repo),
		WithEventStore(store),
		WithOutboxRelay(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fw.Close(context.Background()) }()
	// While the store is down the row must stay unpublished.
	time.Sleep(50 * time.Millisecond)
	if repo.unpublishedCount() != 1 {
		t.Fatalf("failed delivery must leave the row unpublished")
	}
	store.setFailing(false)
	waitForCondition(t, 5*time.Second, "relay recovery", func() bool {
		return repo.unpublishedCount() == 0
	})
	events, err := store.ListEvents(context.Background(), "run-1", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one delivered event after retries, got %+v", events)
	}
}

// A row whose event already exists at (run_id, sequence) — delivered by an
// earlier attempt that crashed before marking — is treated as delivered and
// marked published without duplicating.
func TestOutboxRelayTreatsConflictAsDelivered(t *testing.T) {
	repo := newFakeOutboxRepo()
	store := &fakeSequencedStore{Store: observabilityinmem.NewStore()}
	event := core.Event{Type: core.EventRunCompleted, RunID: "run-1", Timestamp: time.Now().UTC()}
	if _, err := store.AppendSequenced(context.Background(), 4, event); err != nil {
		t.Fatal(err)
	}
	repo.park(4, event)
	fw, err := New(outboxTestScenario(),
		WithRunStateRepository(repo),
		WithEventStore(store),
		WithOutboxRelay(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fw.Close(context.Background()) }()
	waitForCondition(t, 5*time.Second, "conflict marking", func() bool {
		return repo.unpublishedCount() == 0
	})
	events, err := store.ListEvents(context.Background(), "run-1", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("conflicting redelivery must not duplicate, got %+v", events)
	}
}

// Two frameworks relaying the same database deliver every row effectively
// once: conditional marking plus (run_id, sequence) dedup.
func TestOutboxRelayConcurrentFrameworks(t *testing.T) {
	repo := newFakeOutboxRepo()
	store := &fakeSequencedStore{Store: observabilityinmem.NewStore()}
	const total = 20
	for i := 1; i <= total; i++ {
		repo.park(int64(i), core.Event{Type: core.EventType("Custom"), RunID: "run-1", Timestamp: time.Now().UTC()})
	}
	newRelay := func() *Framework {
		fw, err := New(outboxTestScenario(),
			WithRunStateRepository(repo),
			WithEventStore(store),
			WithOutboxRelay(2*time.Millisecond),
		)
		if err != nil {
			t.Fatal(err)
		}
		return fw
	}
	fw1 := newRelay()
	fw2 := newRelay()
	defer func() {
		_ = fw1.Close(context.Background())
		_ = fw2.Close(context.Background())
	}()
	waitForCondition(t, 10*time.Second, "concurrent relay drain", func() bool {
		return repo.unpublishedCount() == 0
	})
	events, err := store.ListEvents(context.Background(), "run-1", obspkg.EventQuery{Limit: obspkg.MaxEventQueryLimit})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != total {
		t.Fatalf("expected %d events delivered exactly once, got %d", total, len(events))
	}
}

func TestPurgeRunsCascadesEventsAndOutbox(t *testing.T) {
	ctx := context.Background()
	repo := newFakeOutboxRepo()
	store := observabilityinmem.NewStore()
	fw, err := New(outboxTestScenario(),
		WithRunStateRepository(repo),
		WithEventStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "run-dead", ScenarioName: "outbox-test", Status: runstate.RunStatusCompleted}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-dead", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-alive", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	repo.park(3, core.Event{Type: core.EventRunCompleted, RunID: "run-dead", Timestamp: time.Now().UTC()})
	repo.park(1, core.Event{Type: core.EventRunStarted, RunID: "run-alive", Timestamp: time.Now().UTC()})
	removed, err := fw.PurgeRuns(ctx, runstate.ListFilter{ScenarioName: "outbox-test"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 run purged, got %d", removed)
	}
	events, err := store.ListEvents(ctx, "run-dead", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events of the deleted run must be cascaded, got %+v", events)
	}
	events, err = store.ListEvents(ctx, "run-alive", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events of other runs must survive, got %+v", events)
	}
	if repo.rowCount() != 1 {
		t.Fatalf("outbox rows of the deleted run must be cascaded, %d rows left", repo.rowCount())
	}
	if repo.rows[0].Event.RunID != "run-alive" {
		t.Fatalf("outbox rows of other runs must survive, got %+v", repo.rows[0])
	}
}

func TestPurgeExpiredPurgesEventSideData(t *testing.T) {
	ctx := context.Background()
	repo := newFakeOutboxRepo()
	store := observabilityinmem.NewStore()
	fw, err := New(outboxTestScenario(),
		WithRunStateRepository(repo),
		WithEventStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "run-old", ScenarioName: "outbox-test", Status: runstate.RunStatusCompleted}, 0); err != nil {
		t.Fatal(err)
	}
	oldEvent := core.Event{Type: core.EventRunStarted, RunID: "run-elsewhere", Timestamp: time.Now().UTC().Add(-time.Hour)}
	if _, err := store.Append(ctx, oldEvent); err != nil {
		t.Fatal(err)
	}
	parkedID := repo.park(3, oldEvent)
	if err := repo.MarkOutboxPublished(ctx, parkedID); err != nil {
		t.Fatal(err)
	}
	// Backdate the publication so the age purge collects the row.
	repo.mu.Lock()
	repo.publishedAt[parkedID] = time.Now().UTC().Add(-time.Hour)
	repo.mu.Unlock()
	repo.park(4, core.Event{Type: core.EventRunStarted, RunID: "run-elsewhere", Timestamp: time.Now().UTC()}) // stays unpublished: undelivered, never purged by age

	// Age the run past maxAge; the fresh event is appended only afterwards
	// so its timestamp lands after the purge cutoff.
	time.Sleep(1100 * time.Millisecond)
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunCompleted, RunID: "run-elsewhere", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeExpired(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 expired run, got %d", removed)
	}
	events, err := store.ListEvents(ctx, "run-elsewhere", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.Timestamp.Before(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("only the fresh event must survive the age purge, got %+v", events)
	}
	if repo.rowCount() != 1 || repo.unpublishedCount() != 1 {
		t.Fatalf("published old outbox rows must be purged, unpublished kept: rows=%d unpublished=%d", repo.rowCount(), repo.unpublishedCount())
	}
}

func TestPurgeRunsWithoutEventStoreSkipsCascade(t *testing.T) {
	ctx := context.Background()
	repo := newFakeOutboxRepo()
	fw, err := New(outboxTestScenario(), WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "run-1", ScenarioName: "outbox-test", Status: runstate.RunStatusCompleted}, 0); err != nil {
		t.Fatal(err)
	}
	repo.park(1, core.Event{Type: core.EventRunStarted, RunID: "run-1", Timestamp: time.Now().UTC()})
	removed, err := fw.PurgeRuns(ctx, runstate.ListFilter{ScenarioName: "outbox-test"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 run purged, got %d", removed)
	}
	// The outbox cascade works off the repository even without an event
	// store wired.
	if repo.rowCount() != 0 {
		t.Fatalf("outbox rows must be cascaded via the repository, %d left", repo.rowCount())
	}
}
