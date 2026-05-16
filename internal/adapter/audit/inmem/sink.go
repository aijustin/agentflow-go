package inmem

import (
	"context"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
)

type Sink struct {
	mu     sync.RWMutex
	events []audit.Event
	limit  int
	now    func() time.Time
}

func NewSink(limit int) *Sink {
	return &Sink{limit: limit, now: func() time.Time { return time.Now().UTC() }}
}

func (sink *Sink) Record(ctx context.Context, event audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event = audit.CloneEvent(event).WithDefaults(sink.now())
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, event)
	if sink.limit > 0 && len(sink.events) > sink.limit {
		sink.events = append([]audit.Event(nil), sink.events[len(sink.events)-sink.limit:]...)
	}
	return nil
}

func (sink *Sink) Events() []audit.Event {
	sink.mu.RLock()
	defer sink.mu.RUnlock()
	events := make([]audit.Event, len(sink.events))
	for i, event := range sink.events {
		events[i] = audit.CloneEvent(event)
	}
	return events
}
