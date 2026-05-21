package ticket

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/toolticket"
)

type Request struct {
	Action string            `json:"action"`
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields,omitempty"`
	Author string            `json:"author,omitempty"`
	Body   string            `json:"body,omitempty"`
}

type Executor struct {
	store toolticket.Store
}

func NewExecutor(store toolticket.Store) (*Executor, error) {
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
		ticket toolticket.Ticket
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

func NewMemoryStore(seed map[string]toolticket.Ticket) *toolticket.MemoryStore {
	return toolticket.NewMemoryStore(seed)
}
