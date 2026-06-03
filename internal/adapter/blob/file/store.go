package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aijustin/agentflow-go/internal/fsatomic"
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
	if err := fsatomic.WriteFile(path, data, 0o600); err != nil {
		return runstate.BlobRef{}, err
	}
	return runstate.BlobRef{ID: id, Size: int64(len(data)), Sha256: id}, nil
}

func (s *Store) Get(ctx context.Context, ref runstate.BlobRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.ID == "" {
		return nil, fmt.Errorf("blob file: id is required")
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

func (s *Store) List(ctx context.Context) ([]runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]runstate.BlobRef, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".blob" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".blob")
		// The file name is the content's sha256 (see Put), so listing does not
		// need to read every blob body just to derive its ref; the size comes
		// from the directory entry's metadata. Get still verifies the hash.
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("blob file: stat %s: %w", entry.Name(), err)
		}
		out = append(out, runstate.BlobRef{ID: id, Size: info.Size(), Sha256: id})
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ref runstate.BlobRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.ID == "" {
		return fmt.Errorf("blob file: id is required")
	}
	err := os.Remove(filepath.Join(s.dir, ref.ID+".blob"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
