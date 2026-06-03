package inmem

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

type CognitiveRepository struct {
	mu      sync.RWMutex
	records map[string][]memory.CognitiveRecord
}

func NewCognitiveRepository() *CognitiveRepository {
	return &CognitiveRepository{records: make(map[string][]memory.CognitiveRecord)}
}

func (r *CognitiveRepository) Remember(_ context.Context, ns memory.Namespace, record memory.CognitiveRecord) error {
	if record.ID == "" {
		return fmt.Errorf("cognitive memory: id is required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	key := ns.KeyPrefix()
	r.mu.Lock()
	defer r.mu.Unlock()
	items := append([]memory.CognitiveRecord(nil), r.records[key]...)
	for index := range items {
		if items[index].ID == record.ID {
			items[index] = record
			r.records[key] = items
			return nil
		}
	}
	r.records[key] = append(items, record)
	return nil
}

func (r *CognitiveRepository) Recall(_ context.Context, ns memory.Namespace, query string, limit int) ([]memory.CognitiveRecord, error) {
	if limit <= 0 {
		limit = 5
	}
	key := ns.KeyPrefix()
	r.mu.RLock()
	items := append([]memory.CognitiveRecord(nil), r.records[key]...)
	r.mu.RUnlock()
	ranked := memory.RankMemories(query, items, time.Now().UTC(), 0.5, 0.3, 0.2)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]memory.CognitiveRecord, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

var _ memory.CognitiveMemory = (*CognitiveRepository)(nil)

// AppendCognitiveFromMessages stores assistant/user turns into cognitive memory.
func AppendCognitiveFromMessages(repo memory.CognitiveMemory, ctx context.Context, ns memory.Namespace, role, content string, importance float64) error {
	if repo == nil || content == "" {
		return nil
	}
	return repo.Remember(ctx, ns, memory.CognitiveRecord{
		ID:         fmt.Sprintf("%s:%d", role, time.Now().UTC().UnixNano()),
		Content:    content,
		Scope:      string(ns.Scope),
		Importance: importance,
		Categories: []string{role},
	})
}
