package toolticket

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Ticket is a support ticket record manipulated by the ticket tool.
type Ticket struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Status      string            `json:"status"`
	Description string            `json:"description,omitempty"`
	Comments    []Comment         `json:"comments,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Comment is a ticket comment entry.
type Comment struct {
	Body      string    `json:"body"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store persists ticket records for the ticket tool executor.
type Store interface {
	Get(ctx context.Context, id string) (Ticket, error)
	Update(ctx context.Context, id string, fields map[string]string) (Ticket, error)
	AddComment(ctx context.Context, id, author, body string) (Ticket, error)
}

// MemoryStore is an in-memory Store for tests and demos.
type MemoryStore struct {
	mu      sync.RWMutex
	tickets map[string]Ticket
}

// NewMemoryStore creates an in-memory ticket store optionally seeded with records.
func NewMemoryStore(seed map[string]Ticket) *MemoryStore {
	tickets := make(map[string]Ticket, len(seed))
	for id, ticket := range seed {
		if ticket.ID == "" {
			ticket.ID = id
		}
		tickets[id] = ticket
	}
	return &MemoryStore{tickets: tickets}
}

func (s *MemoryStore) Get(_ context.Context, id string) (Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ticket, ok := s.tickets[id]
	if !ok {
		return Ticket{}, fmt.Errorf("ticket %q not found", id)
	}
	return ticket, nil
}

func (s *MemoryStore) Update(_ context.Context, id string, fields map[string]string) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[id]
	if !ok {
		return Ticket{}, fmt.Errorf("ticket %q not found", id)
	}
	for key, value := range fields {
		switch key {
		case "title":
			ticket.Title = value
		case "status":
			ticket.Status = value
		case "description":
			ticket.Description = value
		default:
			if ticket.Metadata == nil {
				ticket.Metadata = make(map[string]string)
			}
			ticket.Metadata[key] = value
		}
	}
	ticket.UpdatedAt = time.Now().UTC()
	s.tickets[id] = ticket
	return ticket, nil
}

func (s *MemoryStore) AddComment(_ context.Context, id, author, body string) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[id]
	if !ok {
		return Ticket{}, fmt.Errorf("ticket %q not found", id)
	}
	ticket.Comments = append(ticket.Comments, Comment{
		Body:      body,
		Author:    author,
		CreatedAt: time.Now().UTC(),
	})
	ticket.UpdatedAt = time.Now().UTC()
	s.tickets[id] = ticket
	return ticket, nil
}
