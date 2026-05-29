package tier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

type defaultManager struct {
	store       Store
	policy      Policy
	observer    MigrationObserver
	weights     memory.RecallWeights
	coldSummary ColdSummaryBackend

	// reconcileLocks serializes Reconcile per namespace so concurrent
	// reconciliation passes do not make tier-cap decisions from each other's
	// stale level counts.
	reconcileMu    sync.Mutex
	reconcileLocks map[string]*sync.Mutex
}

// NewManager constructs a tier Manager backed by store and policy.
func NewManager(store Store, policy Policy, observer MigrationObserver) Manager {
	return NewManagerWithWeights(store, policy, observer, memory.DefaultRecallWeights(), NoopColdSummaryBackend{})
}

// NewManagerWithWeights constructs a tier Manager with custom RankMemories weights.
func NewManagerWithWeights(store Store, policy Policy, observer MigrationObserver, weights memory.RecallWeights, coldSummary ColdSummaryBackend) Manager {
	if observer == nil {
		observer = NoopMigrationObserver{}
	}
	if coldSummary == nil {
		coldSummary = NoopColdSummaryBackend{}
	}
	return &defaultManager{
		store:          store,
		policy:         policy,
		observer:       observer,
		weights:        weights.Normalize(),
		coldSummary:    coldSummary,
		reconcileLocks: make(map[string]*sync.Mutex),
	}
}

// reconcileLock returns the per-namespace mutex used to serialize Reconcile.
func (m *defaultManager) reconcileLock(ns memory.Namespace) *sync.Mutex {
	key := ns.KeyPrefix()
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	if m.reconcileLocks == nil {
		m.reconcileLocks = make(map[string]*sync.Mutex)
	}
	lock, ok := m.reconcileLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		m.reconcileLocks[key] = lock
	}
	return lock
}

func (m *defaultManager) Remember(ctx context.Context, ns memory.Namespace, record Record) error {
	now := time.Now().UTC()
	if record.Tier == "" {
		record.Tier = LevelHot
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.LastAccessAt.IsZero() {
		record.LastAccessAt = now
	}
	if record.ID == "" {
		id, err := newRecordID()
		if err != nil {
			return err
		}
		record.ID = id
	}
	if record.Importance <= 0 {
		record.Importance = 0.5
	}
	return m.store.Put(ctx, ns, record)
}

func (m *defaultManager) Recall(ctx context.Context, ns memory.Namespace, query string, budget RecallBudget) ([]Record, error) {
	budget = budget.Normalize()
	now := time.Now().UTC()

	candidates := make([]Record, 0, budget.Total*2)
	seen := make(map[string]struct{})
	appendCandidate := func(record Record) {
		if _, ok := seen[record.ID]; ok {
			return
		}
		seen[record.ID] = struct{}{}
		candidates = append(candidates, record)
	}
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		limit := budgetForLevel(budget, level)
		if limit <= 0 {
			continue
		}
		records, err := m.store.List(ctx, ns, level, limit*2)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			appendCandidate(record)
		}
	}
	if budget.Cold > 0 && strings.TrimSpace(query) != "" && m.coldSummary != nil {
		ids, err := m.coldSummary.SearchRecordIDs(ctx, ns, query, budget.Cold*2)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			record, err := m.store.Get(ctx, ns, id)
			if err != nil {
				if errors.Is(err, memory.ErrNotFound) {
					continue
				}
				return nil, err
			}
			if record.Tier == LevelCold {
				appendCandidate(record)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	cogRecords := make([]memory.CognitiveRecord, len(candidates))
	for i, record := range candidates {
		cogRecords[i] = searchableCognitiveRecord(record)
	}
	ranked := memory.RankMemories(query, cogRecords, now, m.weights.Semantic, m.weights.Recency, m.weights.Importance)
	byID := make(map[string]Record, len(candidates))
	for _, record := range candidates {
		byID[record.ID] = record
	}

	result := make([]Record, 0, budget.Total)
	for _, score := range ranked {
		if len(result) >= budget.Total {
			break
		}
		record, ok := byID[score.Record.ID]
		if !ok {
			continue
		}
		record.AccessCount++
		record.LastAccessAt = now
		if err := m.applyTargetTier(ctx, ns, &record, now); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (m *defaultManager) Reconcile(ctx context.Context, ns memory.Namespace, now time.Time) (MigrationReport, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lock := m.reconcileLock(ns)
	lock.Lock()
	defer lock.Unlock()
	var report MigrationReport
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		records, err := m.store.List(ctx, ns, level, 0)
		if err != nil {
			return report, err
		}
		counts, err := m.levelCounts(ctx, ns)
		if err != nil {
			return report, err
		}
		for i := range records {
			record := records[i]
			if record.Tier == LevelCold && m.policy.ShouldDemote(record, now, counts[LevelCold]) {
				if err := m.coldSummary.Delete(ctx, ns, record.ID); err != nil {
					return report, err
				}
				if err := m.store.Delete(ctx, ns, record.ID); err != nil {
					return report, err
				}
				m.observer.Evicted(ctx, ns, record.ID, LevelCold)
				report.Evicted++
				counts[LevelCold]--
				continue
			}
			before := record.Tier
			if err := m.applyTargetTier(ctx, ns, &record, now); err != nil {
				return report, err
			}
			switch {
			case record.Tier == before:
				continue
			case tierRank(record.Tier) > tierRank(before):
				report.Promoted++
			default:
				report.Demoted++
			}
			counts[record.Tier]++
			if before != record.Tier {
				counts[before]--
			}
		}
	}
	return report, nil
}

func (m *defaultManager) applyTargetTier(ctx context.Context, ns memory.Namespace, record *Record, now time.Time) error {
	counts, err := m.levelCounts(ctx, ns)
	if err != nil {
		return err
	}
	from := record.Tier
	target := m.policy.TargetTier(*record, now, counts)
	if target == from {
		return m.store.Put(ctx, ns, *record)
	}
	record.Tier = target
	record.PromotedAt = now
	if target == LevelCold && tierRank(from) > tierRank(target) {
		if err := m.coldSummary.Archive(ctx, ns, record); err != nil {
			return err
		}
	}
	if err := m.store.Put(ctx, ns, *record); err != nil {
		return err
	}
	switch {
	case tierRank(target) > tierRank(from):
		m.observer.Promoted(ctx, ns, record.ID, from, target)
	case tierRank(target) < tierRank(from):
		m.observer.Demoted(ctx, ns, record.ID, from, target)
	}
	return nil
}

// ListAll returns every record stored for ns across all tiers, de-duplicated by
// ID (a record briefly visible in two tiers during migration is returned once,
// preferring the highest tier). It backs RebuildIndex.
func (m *defaultManager) ListAll(ctx context.Context, ns memory.Namespace) ([]Record, error) {
	seen := make(map[string]struct{})
	out := make([]Record, 0)
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		records, err := m.store.List(ctx, ns, level, 0)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.ID != "" {
				if _, dup := seen[record.ID]; dup {
					continue
				}
				seen[record.ID] = struct{}{}
			}
			out = append(out, record)
		}
	}
	return out, nil
}

func (m *defaultManager) levelCounts(ctx context.Context, ns memory.Namespace) (map[Level]int, error) {
	counts := make(map[Level]int, 3)
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		count, err := m.store.Count(ctx, ns, level)
		if err != nil {
			return nil, err
		}
		counts[level] = count
	}
	return counts, nil
}

func budgetForLevel(budget RecallBudget, level Level) int {
	switch level {
	case LevelHot:
		return budget.Hot
	case LevelWarm:
		return budget.Warm
	case LevelCold:
		return budget.Cold
	default:
		return 0
	}
}

func tierRank(level Level) int {
	switch level {
	case LevelHot:
		return 3
	case LevelWarm:
		return 2
	case LevelCold:
		return 1
	default:
		return 0
	}
}

func newRecordID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("tier: generate record id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
