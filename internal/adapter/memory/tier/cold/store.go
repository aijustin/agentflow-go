package cold

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

const level = tier.LevelCold

type Store struct {
	mu  sync.RWMutex
	dir string
}

func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("cold tier store: dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Put(ctx context.Context, ns memory.Namespace, record tier.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.ID == "" {
		return fmt.Errorf("cold tier store: record id is required")
	}
	record.Tier = level
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("cold tier store: marshal record %q: %w", record.ID, err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return fmt.Errorf("cold tier store: gzip record %q: %w", record.ID, err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("cold tier store: close gzip record %q: %w", record.ID, err)
	}
	path := s.path(ns, record.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) Get(ctx context.Context, ns memory.Namespace, id string) (tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return tier.Record{}, err
	}
	data, err := os.ReadFile(s.path(ns, id))
	if err != nil {
		if os.IsNotExist(err) {
			return tier.Record{}, memory.ErrNotFound
		}
		return tier.Record{}, err
	}
	raw, err := gunzip(data)
	if err != nil {
		return tier.Record{}, fmt.Errorf("cold tier store: gunzip record %q: %w", id, err)
	}
	var record tier.Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return tier.Record{}, fmt.Errorf("cold tier store: decode record %q: %w", id, err)
	}
	return record, nil
}

func (s *Store) List(ctx context.Context, ns memory.Namespace, level tier.Level, limit int) ([]tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if level != tier.LevelCold {
		return nil, nil
	}
	dir := s.namespaceDir(ns)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type item struct {
		record tier.Record
	}
	items := make([]item, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		raw, err := gunzip(data)
		if err != nil {
			return nil, err
		}
		var record tier.Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		items = append(items, item{record: record})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].record.LastAccessAt.After(items[j].record.LastAccessAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]tier.Record, len(items))
	for i, item := range items {
		out[i] = item.record
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ns memory.Namespace, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := s.path(ns, id)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return memory.ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Store) Count(ctx context.Context, ns memory.Namespace, level tier.Level) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if level != tier.LevelCold {
		return 0, nil
	}
	records, err := s.List(ctx, ns, tier.LevelCold, 0)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func (s *Store) path(ns memory.Namespace, id string) string {
	return filepath.Join(s.namespaceDir(ns), safeFilename(id)+".json.gz")
}

func (s *Store) namespaceDir(ns memory.Namespace) string {
	return filepath.Join(s.dir, safeFilename(ns.KeyPrefix()))
}

func safeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func gunzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
