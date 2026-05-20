package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type Ticket struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Status      string            `json:"status"`
	Description string            `json:"description,omitempty"`
	Comments    []Comment         `json:"comments,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type Comment struct {
	Body      string    `json:"body"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	Get(ctx context.Context, id string) (Ticket, error)
	Update(ctx context.Context, id string, fields map[string]string) (Ticket, error)
	AddComment(ctx context.Context, id, author, body string) (Ticket, error)
}

type Request struct {
	Action string            `json:"action"`
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields,omitempty"`
	Author string            `json:"author,omitempty"`
	Body   string            `json:"body,omitempty"`
}

type Executor struct {
	store Store
}

func NewExecutor(store Store) (*Executor, error) {
	if store == nil {
		return nil, fmt.Errorf("ticket tool: store is required")
	}
	return &Executor{store: store}, nil
}

func (e *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	var req Request
	if err := json.Unmarshal(call.Input, &req); err != nil {
		return core.ToolResult{}, fmt.Errorf("ticket tool: decode input: %w", err)
	}
	if req.ID == "" {
		return core.ToolResult{}, fmt.Errorf("ticket tool: id is required")
	}
	var (
		ticket Ticket
		err    error
	)
	switch req.Action {
	case "get":
		ticket, err = e.store.Get(ctx, req.ID)
	case "update":
		ticket, err = e.store.Update(ctx, req.ID, req.Fields)
	case "comment":
		ticket, err = e.store.AddComment(ctx, req.ID, req.Author, req.Body)
	default:
		return core.ToolResult{}, fmt.Errorf("ticket tool: unsupported action %q", req.Action)
	}
	if err != nil {
		return core.ToolResult{Tool: call.Tool, Error: err.Error()}, nil
	}
	raw, err := json.Marshal(ticket)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: raw}, nil
}

type MemoryStore struct {
	mu      sync.RWMutex
	tickets map[string]Ticket
}

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
