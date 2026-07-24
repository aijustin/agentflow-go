package inmem

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

type Option func(*Store)

type Store struct {
	mu      sync.RWMutex
	now     func() time.Time
	nextID  int64
	nextSeq map[string]int64
	records []obspkg.EventRecord
}

func NewStore(opts ...Option) *Store {
	store := &Store{
		now:     func() time.Time { return time.Now().UTC() },
		nextSeq: make(map[string]int64),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(store)
		}
	}
	return store
}

func WithNow(now func() time.Time) Option {
	return func(store *Store) {
		if now != nil {
			store.now = now
		}
	}
}

func (store *Store) Append(ctx context.Context, event core.Event) (obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return obspkg.EventRecord{}, err
	}
	if event.RunID == "" {
		return obspkg.EventRecord{}, fmt.Errorf("observability inmem: run id is required")
	}
	now := store.now().UTC()
	event = obspkg.NormalizeEvent(event, now)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextID++
	store.nextSeq[event.RunID]++
	record := obspkg.EventRecord{
		ID:        store.nextID,
		Sequence:  store.nextSeq[event.RunID],
		Event:     event,
		CreatedAt: now,
	}
	store.records = append(store.records, obspkg.CloneEventRecord(record))
	return obspkg.CloneEventRecord(record), nil
}

// AppendSequenced implements obspkg.SequencedEventStore: the event is stored
// with the given per-run sequence (minted earlier by an outbox writer). A
// record already holding (run_id, sequence) means the event was delivered
// before, so it is returned as-is with a nil error — relay redelivery stays
// idempotent, mirroring the PostgreSQL store's UNIQUE (run_id, sequence)
// dedup.
func (store *Store) AppendSequenced(ctx context.Context, sequence int64, event core.Event) (obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return obspkg.EventRecord{}, err
	}
	if event.RunID == "" {
		return obspkg.EventRecord{}, fmt.Errorf("observability inmem: run id is required")
	}
	if sequence <= 0 {
		return obspkg.EventRecord{}, fmt.Errorf("observability inmem: sequence must be positive, got %d", sequence)
	}
	now := store.now().UTC()
	event = obspkg.NormalizeEvent(event, now)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, record := range store.records {
		if record.Event.RunID == event.RunID && record.Sequence == sequence {
			return obspkg.CloneEventRecord(record), nil
		}
	}
	store.nextID++
	if store.nextSeq[event.RunID] < sequence {
		store.nextSeq[event.RunID] = sequence
	}
	record := obspkg.EventRecord{
		ID:        store.nextID,
		Sequence:  sequence,
		Event:     event,
		CreatedAt: now,
	}
	store.records = append(store.records, obspkg.CloneEventRecord(record))
	return obspkg.CloneEventRecord(record), nil
}

// DeleteEventsForRun implements obspkg.EventRunPurger for the retention
// cascade.
func (store *Store) DeleteEventsForRun(ctx context.Context, runID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	kept := store.records[:0]
	var removed int64
	for _, record := range store.records {
		if record.Event.RunID == runID {
			removed++
			continue
		}
		kept = append(kept, record)
	}
	for i := len(kept); i < len(store.records); i++ {
		store.records[i] = obspkg.EventRecord{}
	}
	store.records = kept
	delete(store.nextSeq, runID)
	return removed, nil
}

// PurgeEventsBefore implements obspkg.EventStorePurger: removes events that
// occurred before cutoff.
func (store *Store) PurgeEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	kept := store.records[:0]
	var removed int64
	for _, record := range store.records {
		if record.Event.Timestamp.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, record)
	}
	for i := len(kept); i < len(store.records); i++ {
		store.records[i] = obspkg.EventRecord{}
	}
	store.records = kept
	return removed, nil
}

func (store *Store) ListRuns(ctx context.Context, query obspkg.RunQuery) ([]obspkg.RunSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeRunQuery(query)
	store.mu.RLock()
	defer store.mu.RUnlock()
	byRun := make(map[string]obspkg.RunSummary)
	for _, record := range store.records {
		summary := byRun[record.Event.RunID]
		if summary.RunID == "" {
			summary.RunID = record.Event.RunID
			summary.FirstSeenAt = record.Event.Timestamp
		}
		if summary.ScenarioName == "" {
			summary.ScenarioName = record.Event.ScenarioName
		}
		summary.EventCount++
		summary.LastSeenAt = record.Event.Timestamp
		summary.LastEventType = record.Event.Type
		summary.Status = obspkg.StatusAfterEvent(summary.Status, record.Event.Type)
		byRun[record.Event.RunID] = summary
	}
	runs := make([]obspkg.RunSummary, 0, len(byRun))
	for _, summary := range byRun {
		if query.Status != "" && summary.Status != query.Status {
			continue
		}
		runs = append(runs, summary)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].LastSeenAt.Equal(runs[j].LastSeenAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].LastSeenAt.After(runs[j].LastSeenAt)
	})
	if query.Offset >= len(runs) {
		return []obspkg.RunSummary{}, nil
	}
	runs = runs[query.Offset:]
	if len(runs) > query.Limit {
		runs = runs[:query.Limit]
	}
	return runs, nil
}

func (store *Store) ListEvents(ctx context.Context, runID string, query obspkg.EventQuery) ([]obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeEventQuery(query)
	store.mu.RLock()
	defer store.mu.RUnlock()
	events := make([]obspkg.EventRecord, 0)
	for _, record := range store.records {
		if record.Event.RunID != runID || record.Sequence <= query.AfterSequence {
			continue
		}
		if !obspkg.EventAllowedByPreset(record.Event.Type, query.Preset) {
			continue
		}
		events = append(events, obspkg.CloneEventRecord(record))
		if len(events) >= query.Limit {
			break
		}
	}
	return events, nil
}

func (store *Store) ListScopedEvents(ctx context.Context, query obspkg.ScopedEventQuery) ([]obspkg.EventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = obspkg.NormalizeScopedEventQuery(query)
	if query.EpisodeID == "" && query.SessionID == "" {
		return nil, fmt.Errorf("observability inmem: episode_id or session_id is required")
	}
	if query.EpisodeID != "" && query.SessionID != "" {
		return nil, fmt.Errorf("observability inmem: episode_id and session_id are mutually exclusive")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	events := make([]obspkg.EventRecord, 0)
	for _, record := range store.records {
		if record.ID <= query.AfterID {
			continue
		}
		if query.EpisodeID != "" && record.Event.EpisodeID != query.EpisodeID {
			continue
		}
		if query.SessionID != "" && record.Event.SessionID != query.SessionID {
			continue
		}
		if !obspkg.EventAllowedByPreset(record.Event.Type, query.Preset) {
			continue
		}
		events = append(events, obspkg.CloneEventRecord(record))
		if len(events) >= query.Limit {
			break
		}
	}
	return events, nil
}
