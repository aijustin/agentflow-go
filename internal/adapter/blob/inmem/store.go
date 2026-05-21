package inmem

import (
	"context"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type Store struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewStore() *Store {
	return &Store{data: make(map[string][]byte)}
}

func (s *Store) Put(ctx context.Context, data []byte) (runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return runstate.BlobRef{}, err
	}
	ref := runstate.NewBlobRef("", data)
	ref.ID = ref.Sha256
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[ref.ID] = clone(data)
	return ref, nil
}

func (s *Store) Get(ctx context.Context, ref runstate.BlobRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[ref.ID]
	if !ok {
		return nil, runstate.ErrNotFound
	}
	return clone(data), nil
}

func (s *Store) List(ctx context.Context) ([]runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]runstate.BlobRef, 0, len(s.data))
	for id, data := range s.data {
		out = append(out, runstate.NewBlobRef(id, data))
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ref runstate.BlobRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, ref.ID)
	return nil
}

func clone(value []byte) []byte {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
