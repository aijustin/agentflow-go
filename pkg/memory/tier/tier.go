package tier

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// Level identifies a memory storage and recall tier.
type Level string

const (
	LevelHot  Level = "hot"
	LevelWarm Level = "warm"
	LevelCold Level = "cold"
)

// Record extends a cognitive memory entry with tier lifecycle metadata.
type Record struct {
	memory.CognitiveRecord
	Tier         Level     `json:"tier"`
	AccessCount  int       `json:"access_count"`
	LastAccessAt time.Time `json:"last_access_at"`
	PromotedAt   time.Time `json:"promoted_at"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	Pinned       bool      `json:"pinned,omitempty"`
}

// RecallBudget limits how many records to pull from each tier during recall.
type RecallBudget struct {
	Total int `json:"total"`
	Hot   int `json:"hot"`
	Warm  int `json:"warm"`
	Cold  int `json:"cold"`
}

// Normalize fills zero values with sensible defaults and caps per-tier limits by Total.
func (b RecallBudget) Normalize() RecallBudget {
	if b.Total <= 0 {
		b.Total = 20
	}
	if b.Hot <= 0 {
		b.Hot = b.Total * 3 / 5
	}
	if b.Warm <= 0 {
		b.Warm = b.Total * 1 / 4
	}
	if b.Cold <= 0 {
		b.Cold = b.Total - b.Hot - b.Warm
	}
	if b.Hot+b.Warm+b.Cold > b.Total {
		overflow := b.Hot + b.Warm + b.Cold - b.Total
		if b.Cold >= overflow {
			b.Cold -= overflow
		} else {
			overflow -= b.Cold
			b.Cold = 0
			if b.Warm >= overflow {
				b.Warm -= overflow
			}
		}
	}
	return b
}

// Store persists tier-scoped memory records.
type Store interface {
	Put(ctx context.Context, ns memory.Namespace, record Record) error
	Get(ctx context.Context, ns memory.Namespace, id string) (Record, error)
	List(ctx context.Context, ns memory.Namespace, level Level, limit int) ([]Record, error)
	Delete(ctx context.Context, ns memory.Namespace, id string) error
	Count(ctx context.Context, ns memory.Namespace, level Level) (int, error)
}

// Manager orchestrates tiered remember, recall, and reconciliation.
type Manager interface {
	Remember(ctx context.Context, ns memory.Namespace, record Record) error
	Recall(ctx context.Context, ns memory.Namespace, query string, budget RecallBudget) ([]Record, error)
	Reconcile(ctx context.Context, ns memory.Namespace, now time.Time) (MigrationReport, error)
}

// RecordEnumerator is an optional Manager capability that lists every stored
// record in a namespace across all tiers. It backs index rebuild/reconciliation.
type RecordEnumerator interface {
	ListAll(ctx context.Context, ns memory.Namespace) ([]Record, error)
}

// IndexRebuilder is an optional Manager capability that re-mirrors the durable
// tier records into a secondary index (e.g. a cognitive search index), used to
// converge the index after a transient mirror failure. It returns the number of
// records re-mirrored.
type IndexRebuilder interface {
	RebuildIndex(ctx context.Context, ns memory.Namespace) (int, error)
}

// MigrationReport summarizes tier migrations from a reconcile pass.
type MigrationReport struct {
	Promoted int `json:"promoted"`
	Demoted  int `json:"demoted"`
	Evicted  int `json:"evicted"`
}

// DefaultPolicy returns baseline hot/warm/cold promotion and demotion rules.
func DefaultPolicy() Policy {
	return Policy{
		HotCapacity:   50,
		WarmCapacity:  500,
		ColdCapacity:  5000,
		HotTTL:        24 * time.Hour,
		WarmTTL:       30 * 24 * time.Hour,
		PromoteAccess: 3,
		DemoteIdle:    7 * 24 * time.Hour,
	}
}
