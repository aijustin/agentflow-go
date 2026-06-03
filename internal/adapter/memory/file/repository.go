package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/aijustin/agentflow-go/internal/fsatomic"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type Repository struct {
	dir string
	mu  sync.Mutex
}

func NewRepository(dir string) (*Repository, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Repository{dir: dir}, nil
}

func (r *Repository) Get(ctx context.Context, ns memory.Namespace, key string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := os.ReadFile(r.path(ns, key))
	if os.IsNotExist(err) {
		return nil, memory.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return clone(data), nil
}

func (r *Repository) Set(ctx context.Context, ns memory.Namespace, key string, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return fsatomic.WriteFile(r.path(ns, key), value, 0o600)
}

func (r *Repository) Append(ctx context.Context, ns memory.Namespace, key string, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	path := r.path(ns, key)
	var values []json.RawMessage
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		if err := json.Unmarshal(existing, &values); err != nil {
			values = []json.RawMessage{clone(existing)}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	values = append(values, clone(value))
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return fsatomic.WriteFile(path, data, 0o600)
}

func (r *Repository) Delete(ctx context.Context, ns memory.Namespace, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	err := os.Remove(r.path(ns, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List is not supported by the file-backed repository because the file name is
// a hash of the full namespace+key and the original key cannot be recovered
// from the hash.  It always returns an empty slice.
func (r *Repository) List(_ context.Context, _ memory.Namespace, _ string) ([]memory.Entry, error) {
	return nil, nil
}

func (r *Repository) path(ns memory.Namespace, key string) string {
	sum := sha256.Sum256([]byte(ns.KeyPrefix() + ":" + key))
	return filepath.Join(r.dir, hex.EncodeToString(sum[:])+".json")
}

func clone(value []byte) []byte {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
