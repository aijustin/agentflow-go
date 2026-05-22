package inmem

import (
	"context"
	"sort"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

// Store is an in-process tier store for tests and examples.
type Store struct {
	mu   sync.RWMutex
	data map[string]tier.Record
}

func NewStore() *Store {
	return &Store{data: make(map[string]tier.Record)}
}

func (s *Store) Put(ctx context.Context, ns memory.Namespace, record tier.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.ID == "" {
		return memory.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key(ns, record.ID)] = cloneRecord(record)
	return nil
}

func (s *Store) Get(ctx context.Context, ns memory.Namespace, id string) (tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return tier.Record{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.data[key(ns, id)]
	if !ok {
		return tier.Record{}, memory.ErrNotFound
	}
	return cloneRecord(record), nil
}

func (s *Store) List(ctx context.Context, ns memory.Namespace, level tier.Level, limit int) ([]tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix := ns.KeyPrefix() + ":"
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]tier.Record, 0)
	for k, record := range s.data {
		if !hasPrefix(k, prefix) {
			continue
		}
		if record.Tier != level {
			continue
		}
		out = append(out, cloneRecord(record))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAccessAt.Equal(out[j].LastAccessAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].LastAccessAt.After(out[j].LastAccessAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ns memory.Namespace, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key(ns, id))
	return nil
}

func (s *Store) Count(ctx context.Context, ns memory.Namespace, level tier.Level) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	prefix := ns.KeyPrefix() + ":"
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for k, record := range s.data {
		if !hasPrefix(k, prefix) {
			continue
		}
		if record.Tier == level {
			count++
		}
	}
	return count, nil
}

func key(ns memory.Namespace, id string) string {
	return ns.KeyPrefix() + ":" + id
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}

func cloneRecord(record tier.Record) tier.Record {
	if record.Metadata != nil {
		meta := make(map[string]string, len(record.Metadata))
		for k, v := range record.Metadata {
			meta[k] = v
		}
		record.Metadata = meta
	}
	return record
}
