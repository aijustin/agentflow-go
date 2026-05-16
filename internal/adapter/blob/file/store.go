package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Put(ctx context.Context, data []byte) (runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return runstate.BlobRef{}, err
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	path := filepath.Join(s.dir, id+".blob")
	if err := writeAtomic(path, data, 0o600); err != nil {
		return runstate.BlobRef{}, err
	}
	return runstate.BlobRef{ID: id, Size: int64(len(data)), Sha256: id}, nil
}

func (s *Store) Get(ctx context.Context, ref runstate.BlobRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, ref.ID+".blob"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if ref.Sha256 != "" && got != ref.Sha256 {
		return nil, fmt.Errorf("blob file: checksum mismatch for %s", ref.ID)
	}
	if ref.Size > 0 && int64(len(data)) != ref.Size {
		return nil, fmt.Errorf("blob file: size mismatch for %s", ref.ID)
	}
	return data, nil
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
