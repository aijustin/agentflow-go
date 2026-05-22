package tier

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

type testStore struct {
	mu   sync.RWMutex
	data map[string]Record
}

func newTestStore() *testStore {
	return &testStore{data: make(map[string]Record)}
}

func (s *testStore) Put(ctx context.Context, ns memory.Namespace, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[testKey(ns, record.ID)] = record
	return nil
}

func (s *testStore) Get(ctx context.Context, ns memory.Namespace, id string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.data[testKey(ns, id)]
	if !ok {
		return Record{}, memory.ErrNotFound
	}
	return record, nil
}

func (s *testStore) List(ctx context.Context, ns memory.Namespace, level Level, limit int) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix := ns.KeyPrefix() + ":"
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0)
	for key, record := range s.data {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		if record.Tier != level {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastAccessAt.After(out[j].LastAccessAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *testStore) Delete(ctx context.Context, ns memory.Namespace, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, testKey(ns, id))
	return nil
}

func (s *testStore) Count(ctx context.Context, ns memory.Namespace, level Level) (int, error) {
	records, err := s.List(ctx, ns, level, 0)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func testKey(ns memory.Namespace, id string) string {
	return fmt.Sprintf("%s:%s", ns.KeyPrefix(), id)
}
