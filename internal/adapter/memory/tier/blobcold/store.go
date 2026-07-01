package blobcold

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
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const level = tier.LevelCold

// maxDecompressedRecordBytes bounds how large a single record may expand to
// when gunzipped, so a corrupted or maliciously crafted blob cannot exhaust
// memory via gzip-bomb-style decompression (a few KB of gzip input can
// otherwise expand to gigabytes).
const maxDecompressedRecordBytes = 256 << 20

type Config struct {
	Blobs    runstate.BlobStore
	IndexDir string
}

type Store struct {
	mu       sync.RWMutex
	blobs    runstate.BlobAdmin
	indexDir string
}

type namespaceIndex struct {
	Records map[string]runstate.BlobRef `json:"records"`
}

func NewStore(config Config) (*Store, error) {
	admin, ok := config.Blobs.(runstate.BlobAdmin)
	if !ok || admin == nil {
		return nil, fmt.Errorf("blob cold tier store: blob store must implement BlobAdmin")
	}
	indexDir := strings.TrimSpace(config.IndexDir)
	if indexDir == "" {
		return nil, fmt.Errorf("blob cold tier store: index dir is required")
	}
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		return nil, err
	}
	return &Store{blobs: admin, indexDir: indexDir}, nil
}

func (s *Store) Put(ctx context.Context, ns memory.Namespace, record tier.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.ID == "" {
		return fmt.Errorf("blob cold tier store: record id is required")
	}
	record.Tier = level
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("blob cold tier store: marshal record %q: %w", record.ID, err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return fmt.Errorf("blob cold tier store: gzip record %q: %w", record.ID, err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("blob cold tier store: close gzip record %q: %w", record.ID, err)
	}
	ref, err := s.blobs.Put(ctx, buf.Bytes())
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexLocked(ns)
	if err != nil {
		// The blob we just wrote is unreferenced; drop it so it does not leak.
		_ = s.blobs.Delete(ctx, ref)
		return err
	}
	previous, hadPrevious := index.Records[record.ID]
	index.Records[record.ID] = ref
	// Persist the index pointing at the new blob BEFORE deleting the old one.
	// If the save fails we remove the new orphan and keep the previous blob, so
	// a failed write never loses the record.
	if err := s.saveIndexLocked(ns, index); err != nil {
		_ = s.blobs.Delete(ctx, ref)
		return err
	}
	if hadPrevious && previous.ID != "" && previous.ID != ref.ID {
		// Old blob is now unreferenced; a failed delete only leaves an orphan
		// for GC, so it must not fail the Put.
		_ = s.blobs.Delete(ctx, previous)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, ns memory.Namespace, id string) (tier.Record, error) {
	if err := ctx.Err(); err != nil {
		return tier.Record{}, err
	}
	s.mu.RLock()
	index, err := s.loadIndexLocked(ns)
	s.mu.RUnlock()
	if err != nil {
		return tier.Record{}, err
	}
	ref, ok := index.Records[id]
	if !ok {
		return tier.Record{}, memory.ErrNotFound
	}
	data, err := s.blobs.Get(ctx, ref)
	if err != nil {
		return tier.Record{}, err
	}
	raw, err := gunzip(data)
	if err != nil {
		return tier.Record{}, fmt.Errorf("blob cold tier store: gunzip record %q: %w", id, err)
	}
	var record tier.Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return tier.Record{}, fmt.Errorf("blob cold tier store: decode record %q: %w", id, err)
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
	s.mu.RLock()
	index, err := s.loadIndexLocked(ns)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	type item struct {
		record tier.Record
	}
	items := make([]item, 0, len(index.Records))
	for id := range index.Records {
		record, err := s.Get(ctx, ns, id)
		if err != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.loadIndexLocked(ns)
	if err != nil {
		return err
	}
	ref, ok := index.Records[id]
	if !ok {
		return memory.ErrNotFound
	}
	delete(index.Records, id)
	// Remove the reference first so we never leave the index pointing at a
	// deleted blob. A subsequent blob delete failure only leaves an orphan that
	// GC reclaims, so it must not fail the logical delete.
	if err := s.saveIndexLocked(ns, index); err != nil {
		return err
	}
	_ = s.blobs.Delete(ctx, ref)
	return nil
}

func (s *Store) Count(ctx context.Context, ns memory.Namespace, level tier.Level) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if level != tier.LevelCold {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	index, err := s.loadIndexLocked(ns)
	if err != nil {
		return 0, err
	}
	return len(index.Records), nil
}

func (s *Store) indexPath(ns memory.Namespace) string {
	return filepath.Join(s.indexDir, safeFilename(ns.KeyPrefix())+".json")
}

func (s *Store) loadIndexLocked(ns memory.Namespace) (namespaceIndex, error) {
	path := s.indexPath(ns)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return namespaceIndex{Records: make(map[string]runstate.BlobRef)}, nil
		}
		return namespaceIndex{}, err
	}
	var index namespaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return namespaceIndex{}, fmt.Errorf("blob cold tier store: decode index %q: %w", ns.KeyPrefix(), err)
	}
	if index.Records == nil {
		index.Records = make(map[string]runstate.BlobRef)
	}
	return index, nil
}

func (s *Store) saveIndexLocked(ns memory.Namespace, index namespaceIndex) error {
	if index.Records == nil {
		index.Records = make(map[string]runstate.BlobRef)
	}
	raw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	path := s.indexPath(ns)
	tmp := path + ".tmp"
	if err := writeFileSync(tmp, raw, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// writeFileSync writes data to path and fsyncs the file before closing so the
// index contents are durable before the caller renames it into place.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir best-effort fsyncs a directory so a rename survives a crash. Errors
// are ignored because some filesystems/platforms do not support directory
// fsync; the file contents are already durable via writeFileSync above.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
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
	out, err := io.ReadAll(io.LimitReader(reader, maxDecompressedRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxDecompressedRecordBytes {
		return nil, fmt.Errorf("decompressed record exceeds %d bytes", maxDecompressedRecordBytes)
	}
	return out, nil
}
