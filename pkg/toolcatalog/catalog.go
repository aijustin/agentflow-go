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

// DefaultDeferralMinTools is the catalog size below which a configured
// DeferralPolicy advertises every tool instead of deferring: below this
// count the meta-tool round-trips cost more than the schemas they save.
const DefaultDeferralMinTools = 8

// DeferralPolicy gates catalog deferral economics. The zero policy (with
// MinTools normalized to DefaultDeferralMinTools) advertises every tool for
// small catalogs.
type DeferralPolicy struct {
	// MinTools: catalogs with fewer entries are advertised in full (no
	// deferral). Values <= 0 normalize to DefaultDeferralMinTools.
	MinTools int
	// MaxOverheadTokens, when > 0, keeps deferral even for a small catalog
	// once the estimated schema overhead of a full advertisement exceeds this
	// budget. Zero disables the overhead check.
	MaxOverheadTokens int
}

func (p DeferralPolicy) minTools() int {
	if p.MinTools <= 0 {
		return DefaultDeferralMinTools
	}
	return p.MinTools
}

// ShouldDefer reports whether catalog tools should stay deferred given the
// catalog size and the estimated token overhead of advertising every entry.
func (p DeferralPolicy) ShouldDefer(catalogSize, overheadTokens int) bool {
	if catalogSize >= p.minTools() {
		return true
	}
	return p.MaxOverheadTokens > 0 && overheadTokens > p.MaxOverheadTokens
}

// charsPerTokenEstimate is the rough chars-per-token ratio used to estimate
// schema advertisement overhead; it only feeds the DeferralPolicy economy
// check, never a hard context budget.
const charsPerTokenEstimate = 4

// Snapshot is an in-memory catalog with version and TTL metadata.
type Snapshot struct {
	VersionTag string
	CacheTTL   time.Duration
	// Deferral gates deferral economics. nil keeps the legacy behavior: when
	// deferral is enabled, every unpinned catalog tool is deferred regardless
	// of catalog size.
	Deferral *DeferralPolicy
	entries  map[string]Entry
}

// NewSnapshot builds a catalog snapshot from entries. Duplicate names keep
// the last entry.
func NewSnapshot(version string, ttl time.Duration, entries []Entry) *Snapshot {
	return buildSnapshot(version, ttl, entries, nil)
}

// NewSnapshotWithDeferral builds a catalog snapshot with a deferral policy.
func NewSnapshotWithDeferral(version string, ttl time.Duration, entries []Entry, policy DeferralPolicy) *Snapshot {
	return buildSnapshot(version, ttl, entries, &policy)
}

func buildSnapshot(version string, ttl time.Duration, entries []Entry, deferral *DeferralPolicy) *Snapshot {
	m := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		m[entry.Name] = entry
	}
	return &Snapshot{VersionTag: version, CacheTTL: ttl, Deferral: deferral, entries: m}
}

// DeferralConfig returns the configured deferral policy; the second return
// is false when the snapshot uses legacy unconditional deferral.
func (s *Snapshot) DeferralConfig() (DeferralPolicy, bool) {
	if s == nil || s.Deferral == nil {
		return DeferralPolicy{}, false
	}
	return *s.Deferral, true
}

// Size returns the number of catalog entries.
func (s *Snapshot) Size() int {
	if s == nil {
		return 0
	}
	return len(s.entries)
}

// OverheadTokens estimates the token cost of advertising every entry's
// description and input schema at once.
func (s *Snapshot) OverheadTokens() int {
	if s == nil {
		return 0
	}
	chars := 0
	for _, entry := range s.entries {
		chars += len(entry.Name) + len(entry.Description) + len(entry.InputSchema)
	}
	return chars / charsPerTokenEstimate
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
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return 1
	}
	// Single-token queries keep historical substring scoring.
	if len(tokens) == 1 {
		return tokenMatchScore(tokens[0], entry)
	}
	// Multi-word queries: score each whitespace token (OR via sum) and keep a
	// whole-phrase bonus when the full query is an exact/contains hit.
	score := tokenMatchScore(query, entry)
	for _, token := range tokens {
		score += tokenMatchScore(token, entry)
	}
	return score
}

func tokenMatchScore(query string, entry Entry) int {
	if query == "" {
		return 0
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
	mu       sync.RWMutex
	inner    *Snapshot
	deferral *DeferralPolicy
}

func NewMutableSnapshot(version string, ttl time.Duration, entries []Entry) *MutableSnapshot {
	return &MutableSnapshot{inner: NewSnapshot(version, ttl, entries)}
}

// NewMutableSnapshotWithDeferral builds a refreshable snapshot whose deferral
// policy survives Replace refreshes.
func NewMutableSnapshotWithDeferral(version string, ttl time.Duration, entries []Entry, policy DeferralPolicy) *MutableSnapshot {
	return &MutableSnapshot{inner: NewSnapshotWithDeferral(version, ttl, entries, policy), deferral: &policy}
}

func (m *MutableSnapshot) Replace(version string, ttl time.Duration, entries []Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inner = buildSnapshot(version, ttl, entries, m.deferral)
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

// DeferralConfig forwards the deferral policy of the current inner snapshot.
func (m *MutableSnapshot) DeferralConfig() (DeferralPolicy, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.DeferralConfig()
}

// Size returns the entry count of the current inner snapshot.
func (m *MutableSnapshot) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Size()
}

// OverheadTokens estimates the full-advertisement token cost of the current
// inner snapshot.
func (m *MutableSnapshot) OverheadTokens() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.OverheadTokens()
}
