package memory

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrNotFound = errors.New("memory: not found")

type Scope string

const (
	ScopeConversation Scope = "conversation"
	ScopeSession      Scope = "session"
	ScopeLongTerm     Scope = "long_term"
	ScopeAudit        Scope = "audit"
)

type Namespace struct {
	RunID     string `json:"run_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Scope     Scope  `json:"scope"`
}

func (n Namespace) KeyPrefix() string {
	return string(n.Scope) + ":" + n.SessionID + ":" + n.RunID + ":" + n.Agent
}

type Entry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type Repository interface {
	Get(ctx context.Context, ns Namespace, key string) (json.RawMessage, error)
	Set(ctx context.Context, ns Namespace, key string, value json.RawMessage) error
	Append(ctx context.Context, ns Namespace, key string, value json.RawMessage) error
	Delete(ctx context.Context, ns Namespace, key string) error
	// List returns all entries whose keys begin with prefix within the
	// namespace. An empty prefix matches all keys in the namespace.
	List(ctx context.Context, ns Namespace, prefix string) ([]Entry, error)
}
