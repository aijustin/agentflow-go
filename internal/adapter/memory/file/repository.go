package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

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
	return writeAtomic(r.path(ns, key), value, 0o600)
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
	return writeAtomic(path, data, 0o600)
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

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
