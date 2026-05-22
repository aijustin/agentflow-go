package tier

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// RecallWeights configures RankMemories weighting during tier recall.
type RecallWeights struct {
	Semantic   float64 `json:"semantic,omitempty" yaml:"semantic"`
	Recency    float64 `json:"recency,omitempty" yaml:"recency"`
	Importance float64 `json:"importance,omitempty" yaml:"importance"`
}

func (w RecallWeights) memoryWeights() memory.RecallWeights {
	return memory.RecallWeights{
		Semantic:   w.Semantic,
		Recency:    w.Recency,
		Importance: w.Importance,
	}.Normalize()
}

// CognitiveAdapter implements memory.CognitiveMemory using a tier Manager.
type CognitiveAdapter struct {
	Manager Manager
	Weights RecallWeights
	Budget  func(limit int) RecallBudget
}

// NewCognitiveAdapter wraps a tier Manager as a CognitiveMemory port.
func NewCognitiveAdapter(manager Manager, weights RecallWeights) *CognitiveAdapter {
	return &CognitiveAdapter{
		Manager: manager,
		Weights: weights,
		Budget: func(limit int) RecallBudget {
			if limit <= 0 {
				limit = 20
			}
			return RecallBudget{Total: limit}.Normalize()
		},
	}
}

func (a *CognitiveAdapter) Remember(ctx context.Context, ns memory.Namespace, record memory.CognitiveRecord) error {
	tierRecord := Record{
		CognitiveRecord: record,
		Tier:            LevelHot,
		LastAccessAt:    time.Now().UTC(),
	}
	return a.Manager.Remember(ctx, ns, tierRecord)
}

func (a *CognitiveAdapter) Recall(ctx context.Context, ns memory.Namespace, query string, limit int) ([]memory.CognitiveRecord, error) {
	budget := a.Budget(limit)
	records, err := a.Manager.Recall(ctx, ns, query, budget)
	if err != nil {
		return nil, err
	}
	out := make([]memory.CognitiveRecord, len(records))
	for i, record := range records {
		out[i] = record.CognitiveRecord
	}
	return out, nil
}

var _ memory.CognitiveMemory = (*CognitiveAdapter)(nil)

type dualWriteManager struct {
	inner Manager
	index memory.CognitiveMemory
}

// NewDualWriteManager remembers tier records and mirrors searchable cognitive entries.
func NewDualWriteManager(inner Manager, index memory.CognitiveMemory) Manager {
	if inner == nil {
		return nil
	}
	return &dualWriteManager{inner: inner, index: index}
}

func (m *dualWriteManager) Remember(ctx context.Context, ns memory.Namespace, record Record) error {
	if err := m.inner.Remember(ctx, ns, record); err != nil {
		return err
	}
	if m.index == nil {
		return nil
	}
	searchable := record.CognitiveRecord
	searchable.Content = memory.SearchableContent(extractSearchable(record))
	return m.index.Remember(ctx, ns, searchable)
}

func (m *dualWriteManager) Recall(ctx context.Context, ns memory.Namespace, query string, budget RecallBudget) ([]Record, error) {
	return m.inner.Recall(ctx, ns, query, budget)
}

func (m *dualWriteManager) Reconcile(ctx context.Context, ns memory.Namespace, now time.Time) (MigrationReport, error) {
	return m.inner.Reconcile(ctx, ns, now)
}

func extractSearchable(record Record) string {
	if text := record.Metadata["searchable"]; text != "" {
		return text
	}
	return record.Content
}

func searchableCognitiveRecord(record Record) memory.CognitiveRecord {
	cog := record.CognitiveRecord
	cog.Content = extractSearchable(record)
	return cog
}
