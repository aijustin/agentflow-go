package tier_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

type mapStore struct {
	records map[string]tier.Record
}

func (s *mapStore) key(ns memory.Namespace, id string) string {
	return ns.KeyPrefix() + "/" + id
}

func (s *mapStore) Put(_ context.Context, ns memory.Namespace, record tier.Record) error {
	if s.records == nil {
		s.records = make(map[string]tier.Record)
	}
	s.records[s.key(ns, record.ID)] = record
	return nil
}

func (s *mapStore) Get(_ context.Context, ns memory.Namespace, id string) (tier.Record, error) {
	record, ok := s.records[s.key(ns, id)]
	if !ok {
		return tier.Record{}, memory.ErrNotFound
	}
	return record, nil
}

func (s *mapStore) List(_ context.Context, ns memory.Namespace, level tier.Level, _ int) ([]tier.Record, error) {
	out := make([]tier.Record, 0)
	for key, record := range s.records {
		if !strings.HasPrefix(key, ns.KeyPrefix()+"/") || record.Tier != level {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *mapStore) Delete(_ context.Context, ns memory.Namespace, id string) error {
	delete(s.records, s.key(ns, id))
	return nil
}

func (s *mapStore) Count(ctx context.Context, ns memory.Namespace, level tier.Level) (int, error) {
	records, err := s.List(ctx, ns, level, 0)
	return len(records), err
}

type stubColdIndexer struct {
	upserted []string
	searched []string
}

func (s *stubColdIndexer) UpsertSummary(_ context.Context, _ memory.Namespace, record tier.Record, summary string) error {
	s.upserted = append(s.upserted, record.ID+":"+summary)
	return nil
}

func (s *stubColdIndexer) SearchSummaries(_ context.Context, _ memory.Namespace, query string, _ int) ([]string, error) {
	s.searched = append(s.searched, query)
	return []string{"cold-1"}, nil
}

func (s *stubColdIndexer) DeleteSummary(_ context.Context, _ memory.Namespace, _ string) error {
	return nil
}

func TestTruncateColdSummaryBackendArchivesLargeRecords(t *testing.T) {
	ctx := context.Background()
	indexer := &stubColdIndexer{}
	backend := tier.TruncateColdSummaryBackend{
		Settings: tier.ColdSummarySettings{Enabled: true, MinBytes: 10, MaxSummaryChars: 20},
		Vector:   indexer,
	}
	record := tier.Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID:       "cold-1",
			Content:  strings.Repeat("x", 64),
			Metadata: map[string]string{"searchable": strings.Repeat("y", 64)},
		},
	}
	if err := backend.Archive(ctx, memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}, &record); err != nil {
		t.Fatal(err)
	}
	if record.Metadata["cold_summary"] != "true" {
		t.Fatalf("expected cold_summary metadata, got %+v", record.Metadata)
	}
	if len(record.Content) > 25 {
		t.Fatalf("expected truncated content, got len=%d", len(record.Content))
	}
	if len(indexer.upserted) != 1 {
		t.Fatalf("expected vector upsert, got %+v", indexer.upserted)
	}
}

func TestManagerRecallMergesColdVectorHits(t *testing.T) {
	ctx := context.Background()
	store := &mapStore{records: make(map[string]tier.Record)}
	policy := tier.Policy{HotCapacity: 1, WarmCapacity: 1, ColdCapacity: 10, PromoteAccess: 99, DemoteIdle: 0}
	indexer := &stubColdIndexer{}
	manager := tier.NewManagerWithWeights(store, policy, tier.NoopMigrationObserver{}, memory.DefaultRecallWeights(), tier.TruncateColdSummaryBackend{
		Settings: tier.ColdSummarySettings{Enabled: true, MinBytes: 1},
		Vector:   indexer,
	})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}
	record := tier.Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID:         "cold-1",
			Content:    "billing archive details",
			Metadata:   map[string]string{"searchable": "billing"},
			Importance: 0.8,
		},
		Tier: tier.LevelCold,
	}
	if err := store.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	got, err := manager.Recall(ctx, ns, "billing", tier.RecallBudget{Total: 5, Hot: 0, Warm: 0, Cold: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "cold-1" {
		t.Fatalf("expected cold vector hit, got %+v searched=%v", got, indexer.searched)
	}
}
