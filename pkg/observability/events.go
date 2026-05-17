package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const (
	DefaultRunQueryLimit   = 50
	MaxRunQueryLimit       = 200
	DefaultEventQueryLimit = 200
	MaxEventQueryLimit     = 1000
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusPaused    RunStatus = "paused"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type EventRecord struct {
	ID        int64      `json:"id"`
	Sequence  int64      `json:"sequence"`
	Event     core.Event `json:"event"`
	CreatedAt time.Time  `json:"created_at"`
}

type RunSummary struct {
	RunID         string         `json:"run_id"`
	ScenarioName  string         `json:"scenario_name,omitempty"`
	Status        RunStatus      `json:"status"`
	EventCount    int64          `json:"event_count"`
	FirstSeenAt   time.Time      `json:"first_seen_at"`
	LastSeenAt    time.Time      `json:"last_seen_at"`
	LastEventType core.EventType `json:"last_event_type"`
}

type RunQuery struct {
	Status RunStatus
	Limit  int
	Offset int
}

type EventQuery struct {
	AfterSequence int64
	Limit         int
}

type EventStore interface {
	Append(ctx context.Context, event core.Event) (EventRecord, error)
	ListRuns(ctx context.Context, query RunQuery) ([]RunSummary, error)
	ListEvents(ctx context.Context, runID string, query EventQuery) ([]EventRecord, error)
}

type EventPublisher interface {
	PublishEvent(ctx context.Context, record EventRecord) error
}

type EventStoreSink struct {
	store      EventStore
	publishers []EventPublisher
}

func NewEventStoreSink(store EventStore, publishers ...EventPublisher) *EventStoreSink {
	filtered := make([]EventPublisher, 0, len(publishers))
	for _, publisher := range publishers {
		if publisher != nil {
			filtered = append(filtered, publisher)
		}
	}
	return &EventStoreSink{store: store, publishers: filtered}
}

func (sink *EventStoreSink) Emit(ctx context.Context, event core.Event) error {
	if sink == nil || sink.store == nil {
		return fmt.Errorf("observability: event store is nil")
	}
	record, err := sink.store.Append(ctx, event)
	if err != nil {
		return err
	}
	var publishErr error
	for _, publisher := range sink.publishers {
		publishErr = errors.Join(publishErr, publisher.PublishEvent(ctx, record))
	}
	return publishErr
}

type fanoutSink struct {
	sinks []core.EventSink
}

func NewEventFanoutSink(sinks ...core.EventSink) core.EventSink {
	filtered := make([]core.EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &fanoutSink{sinks: filtered}
}

func (sink *fanoutSink) Emit(ctx context.Context, event core.Event) error {
	var joined error
	for _, next := range sink.sinks {
		joined = errors.Join(joined, next.Emit(ctx, event))
	}
	return joined
}

type EventSubscriptionFilter struct {
	RunID  string
	Buffer int
}

type EventSubscription struct {
	Events <-chan EventRecord
	Cancel func()
}

type EventHub struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]eventSubscriber
}

type eventSubscriber struct {
	filter EventSubscriptionFilter
	events chan EventRecord
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[uint64]eventSubscriber)}
}

func (hub *EventHub) Subscribe(ctx context.Context, filter EventSubscriptionFilter) EventSubscription {
	if hub == nil {
		closed := make(chan EventRecord)
		close(closed)
		return EventSubscription{Events: closed, Cancel: func() {}}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if filter.Buffer <= 0 {
		filter.Buffer = 64
	}
	events := make(chan EventRecord, filter.Buffer)
	hub.mu.Lock()
	id := hub.nextID
	hub.nextID++
	hub.subscribers[id] = eventSubscriber{filter: filter, events: events}
	hub.mu.Unlock()
	var once sync.Once
	done := make(chan struct{})
	cancel := func() {
		once.Do(func() {
			hub.mu.Lock()
			if subscriber, ok := hub.subscribers[id]; ok {
				delete(hub.subscribers, id)
				close(subscriber.events)
			}
			hub.mu.Unlock()
			close(done)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	}()
	return EventSubscription{Events: events, Cancel: cancel}
}

func (hub *EventHub) PublishEvent(ctx context.Context, record EventRecord) error {
	if hub == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, subscriber := range hub.subscribers {
		if subscriber.filter.RunID != "" && subscriber.filter.RunID != record.Event.RunID {
			continue
		}
		select {
		case subscriber.events <- CloneEventRecord(record):
		default:
		}
	}
	return nil
}

func NormalizeRunQuery(query RunQuery) RunQuery {
	if query.Limit <= 0 {
		query.Limit = DefaultRunQueryLimit
	}
	if query.Limit > MaxRunQueryLimit {
		query.Limit = MaxRunQueryLimit
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	return query
}

func NormalizeEventQuery(query EventQuery) EventQuery {
	if query.Limit <= 0 {
		query.Limit = DefaultEventQueryLimit
	}
	if query.Limit > MaxEventQueryLimit {
		query.Limit = MaxEventQueryLimit
	}
	if query.AfterSequence < 0 {
		query.AfterSequence = 0
	}
	return query
}

func NormalizeEvent(event core.Event, now time.Time) core.Event {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = now
	}
	event.Timestamp = event.Timestamp.UTC()
	event.Payload = CloneRawMessage(event.Payload)
	return event
}

func StatusAfterEvent(current RunStatus, eventType core.EventType) RunStatus {
	switch eventType {
	case core.EventRunCompleted:
		return RunStatusCompleted
	case core.EventRunFailed:
		return RunStatusFailed
	case core.EventRunPaused:
		return RunStatusPaused
	case core.EventRunStarted, core.EventRunResumed:
		return RunStatusRunning
	default:
		if current == "" {
			return RunStatusRunning
		}
		return current
	}
}

func CloneEventRecord(record EventRecord) EventRecord {
	record.Event = CloneEvent(record.Event)
	return record
}

func CloneEvent(event core.Event) core.Event {
	event.Payload = CloneRawMessage(event.Payload)
	return event
}

func CloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	clone := make(json.RawMessage, len(value))
	copy(clone, value)
	return clone
}
