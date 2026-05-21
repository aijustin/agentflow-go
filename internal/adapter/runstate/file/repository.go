package file

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
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

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := r.loadLocked(snapshot.RunID)
	currentVersion := int64(0)
	if err == nil {
		currentVersion = current.Version
	} else if err != runstate.ErrNotFound {
		return err
	}
	if currentVersion != expectedVersion {
		return runstate.ErrStaleSnapshot
	}
	if snapshot.Version <= expectedVersion {
		snapshot.Version = expectedVersion + 1
	}
	var previous *runstate.RunSnapshot
	if err == nil {
		prev := current
		previous = &prev
	}
	runstate.StampSnapshot(snapshot, previous, time.Now().UTC())
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(r.path(snapshot.RunID), data, 0o600)
}

func (r *Repository) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(runID)
}

func (r *Repository) Delete(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	err := os.Remove(r.path(runID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []runstate.RunSnapshot
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, entry.Name()))
		if err != nil {
			continue
		}
		var snap runstate.RunSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			continue
		}
		if filter.Status != "" && snap.Status != filter.Status {
			continue
		}
		if filter.ScenarioName != "" && snap.ScenarioName != filter.ScenarioName {
			continue
		}
		if filter.TenantID != "" && snap.TenantID != filter.TenantID {
			continue
		}
		out = append(out, snap)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (r *Repository) loadLocked(runID string) (runstate.RunSnapshot, error) {
	data, err := os.ReadFile(r.path(runID))
	if os.IsNotExist(err) {
		return runstate.RunSnapshot{}, runstate.ErrNotFound
	}
	if err != nil {
		return runstate.RunSnapshot{}, err
	}
	var snapshot runstate.RunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return runstate.RunSnapshot{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	return snapshot, nil
}

func (r *Repository) path(runID string) string {
	name := base64.RawURLEncoding.EncodeToString([]byte(runID)) + ".json"
	return filepath.Join(r.dir, name)
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
