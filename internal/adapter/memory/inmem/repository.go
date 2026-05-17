package inmem

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

type Repository struct {
	mu   sync.RWMutex
	data map[string]json.RawMessage
}

func NewRepository() *Repository {
	return &Repository{data: make(map[string]json.RawMessage)}
}

func (r *Repository) Get(ctx context.Context, ns memory.Namespace, key string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.data[r.key(ns, key)]
	if !ok {
		return nil, memory.ErrNotFound
	}
	return clone(value), nil
}

func (r *Repository) Set(ctx context.Context, ns memory.Namespace, key string, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.key(ns, key)] = clone(value)
	return nil
}

func (r *Repository) Append(ctx context.Context, ns memory.Namespace, key string, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(ns, key)
	var values []json.RawMessage
	if existing, ok := r.data[k]; ok && len(existing) > 0 {
		if err := json.Unmarshal(existing, &values); err != nil {
			values = []json.RawMessage{clone(existing)}
		}
	}
	values = append(values, clone(value))
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	r.data[k] = encoded
	return nil
}

func (r *Repository) Delete(ctx context.Context, ns memory.Namespace, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, r.key(ns, key))
	return nil
}

func (r *Repository) List(ctx context.Context, ns memory.Namespace, prefix string) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nsPrefix := ns.KeyPrefix() + ":"
	keyPrefix := nsPrefix + prefix
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memory.Entry
	for k, v := range r.data {
		if !strings.HasPrefix(k, keyPrefix) {
			continue
		}
		rawKey := strings.TrimPrefix(k, nsPrefix)
		out = append(out, memory.Entry{Key: rawKey, Value: clone(v)})
	}
	return out, nil
}

func (r *Repository) key(ns memory.Namespace, key string) string {
	return ns.KeyPrefix() + ":" + key
}

func clone(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
