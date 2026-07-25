package toolcatalog

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// Entry describes one tool available through a deferred catalog.
type Entry struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Server      string               `json:"server,omitempty"`
	RemoteName  string               `json:"remote_name,omitempty"`
	InputSchema json.RawMessage      `json:"input_schema,omitempty"`
	SideEffect  core.SideEffectLevel `json:"side_effect,omitempty"`
	Approval    core.ApprovalPolicy  `json:"approval,omitempty"`
	Tags        []string             `json:"tags,omitempty"`
	// Pin marks a tool as always injected alongside catalog meta-tools.
	Pin bool `json:"pin,omitempty"`
}

// SearchResult is a lightweight catalog hit returned by Search.
type SearchResult struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Server      string   `json:"server,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Catalog exposes keyword search and schema loading for deferred tools.
type Catalog interface {
	Search(query string, limit int) []SearchResult
	Load(names []string) ([]Entry, error)
	Version() string
	TTL() time.Duration
}

// Snapshot is an in-memory catalog with version and TTL metadata.
type Snapshot struct {
	VersionTag string
	CacheTTL   time.Duration
	entries    map[string]Entry
}

// NewSnapshot builds a catalog snapshot from entries. Duplicate names keep
// the last entry.
func NewSnapshot(version string, ttl time.Duration, entries []Entry) *Snapshot {
	m := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		m[entry.Name] = entry
	}
	return &Snapshot{VersionTag: version, CacheTTL: ttl, entries: m}
}

func (s *Snapshot) Search(query string, limit int) []SearchResult {
	if s == nil || len(s.entries) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		result SearchResult
		score  int
	}
	var hits []scored
	for _, entry := range s.entries {
		score := matchScore(q, entry)
		if q != "" && score == 0 {
			continue
		}
		hits = append(hits, scored{
			result: SearchResult{
				Name:        entry.Name,
				Description: entry.Description,
				Server:      entry.Server,
				Tags:        append([]string(nil), entry.Tags...),
			},
			score: score,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].result.Name < hits[j].result.Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]SearchResult, len(hits))
	for i, hit := range hits {
		out[i] = hit.result
	}
	return out
}

func matchScore(query string, entry Entry) int {
	if query == "" {
		return 1
	}
	score := 0
	name := strings.ToLower(entry.Name)
	desc := strings.ToLower(entry.Description)
	if name == query {
		score += 100
	} else if strings.Contains(name, query) {
		score += 50
	}
	if strings.Contains(desc, query) {
		score += 20
	}
	for _, tag := range entry.Tags {
		tag = strings.ToLower(tag)
		if tag == query {
			score += 30
		} else if strings.Contains(tag, query) {
			score += 10
		}
	}
	return score
}

func (s *Snapshot) Load(names []string) ([]Entry, error) {
	if s == nil {
		return nil, nil
	}
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		entry, ok := s.entries[name]
		if !ok {
			return nil, errToolNotFound(name)
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *Snapshot) Version() string {
	if s == nil {
		return ""
	}
	return s.VersionTag
}

func (s *Snapshot) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.CacheTTL
}

// Entry returns one catalog entry by name.
func (s *Snapshot) Entry(name string) (Entry, bool) {
	if s == nil {
		return Entry{}, false
	}
	entry, ok := s.entries[name]
	return entry, ok
}

// MutableSnapshot wraps a catalog snapshot that can be refreshed atomically.
type MutableSnapshot struct {
	mu    sync.RWMutex
	inner *Snapshot
}

func NewMutableSnapshot(version string, ttl time.Duration, entries []Entry) *MutableSnapshot {
	return &MutableSnapshot{inner: NewSnapshot(version, ttl, entries)}
}

func (m *MutableSnapshot) Replace(version string, ttl time.Duration, entries []Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inner = NewSnapshot(version, ttl, entries)
}

func (m *MutableSnapshot) Search(query string, limit int) []SearchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Search(query, limit)
}

func (m *MutableSnapshot) Load(names []string) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Load(names)
}

func (m *MutableSnapshot) Version() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Version()
}

func (m *MutableSnapshot) TTL() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.TTL()
}
