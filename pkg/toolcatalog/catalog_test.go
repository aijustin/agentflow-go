package toolcatalog_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

func TestSnapshotSearchAndLoad(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search documentation", Tags: []string{"read", "docs"}, Pin: true},
		{Name: "sql.query", Description: "Run SQL queries", Tags: []string{"write", "db"}},
	})

	hits := catalog.Search("sql", 5)
	if len(hits) != 1 || hits[0].Name != "sql.query" {
		t.Fatalf("search hits = %+v", hits)
	}

	loaded, err := catalog.Load([]string{"docs.search"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "docs.search" || !loaded[0].Pin {
		t.Fatalf("loaded = %+v", loaded)
	}

	if _, err := catalog.Load([]string{"missing"}); err == nil {
		t.Fatal("expected missing tool error")
	}
}

func TestSnapshotSearchMultiWordTokens(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{
			Name:        "get_movie_box_office_ranking",
			Description: "全国某日「影片」票房排行（单位：万，非元）。sort_by 可选 box_office（默认）。仅用于影片榜。",
			Tags:        []string{"mcp", "bookoffice"},
		},
		{
			Name:        "ho_items_list",
			Description: "查询总部卖品列表",
			Tags:        []string{"mcp", "vista"},
		},
	})

	zhHits := catalog.Search("票房排名 影片 票房排行", 5)
	if len(zhHits) == 0 || zhHits[0].Name != "get_movie_box_office_ranking" {
		t.Fatalf("chinese multi-word search = %+v", zhHits)
	}

	enHits := catalog.Search("film box office ranking", 5)
	if len(enHits) == 0 || enHits[0].Name != "get_movie_box_office_ranking" {
		t.Fatalf("english multi-word search = %+v", enHits)
	}

	single := catalog.Search("票房", 5)
	if len(single) == 0 || single[0].Name != "get_movie_box_office_ranking" {
		t.Fatalf("single-token search = %+v", single)
	}
}

func TestMetaToolSpecs(t *testing.T) {
	specs := toolcatalog.MetaToolSpecs()
	if len(specs) != 2 || specs[0].Name != toolcatalog.ToolSearchTools || specs[1].Name != toolcatalog.ToolLoadSchemas {
		t.Fatalf("meta specs = %+v", specs)
	}
	compact := toolcatalog.SelfCompactMetaToolSpec()
	if compact.Name != toolcatalog.ToolCompactContext || compact.Schema == nil {
		t.Fatalf("self compact spec = %+v", compact)
	}
	if toolcatalog.SelfCompactRubric() == "" {
		t.Fatal("expected self compact rubric")
	}
}

func TestSnapshotMetadataEntryAndDefaults(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v2", 2*time.Minute, []toolcatalog.Entry{
		{Name: "z.tool", Description: "database helper", Tags: []string{"data"}},
		{Name: "a.tool", Description: "database helper", Tags: []string{"data"}},
		{Name: "a.tool", Description: "replacement", Tags: []string{"exact"}},
		{Name: ""},
	})
	if catalog.Version() != "v2" || catalog.TTL() != 2*time.Minute {
		t.Fatalf("unexpected metadata: version=%q ttl=%s", catalog.Version(), catalog.TTL())
	}
	entry, ok := catalog.Entry("a.tool")
	if !ok || entry.Description != "replacement" {
		t.Fatalf("duplicate replacement or entry lookup failed: %+v ok=%v", entry, ok)
	}
	hits := catalog.Search("", 0)
	if len(hits) != 2 || hits[0].Name != "a.tool" || hits[1].Name != "z.tool" {
		t.Fatalf("default search ordering = %+v", hits)
	}
	if loaded, err := catalog.Load([]string{"", " a.tool "}); err != nil || len(loaded) != 1 {
		t.Fatalf("trimmed load = %+v err=%v", loaded, err)
	}
	if _, err := catalog.Load([]string{"missing"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing error = %v", err)
	}

	var nilSnapshot *toolcatalog.Snapshot
	if nilSnapshot.Search("x", 1) != nil {
		t.Fatal("nil snapshot search must be empty")
	}
	if loaded, err := nilSnapshot.Load([]string{"x"}); err != nil || loaded != nil {
		t.Fatalf("nil snapshot load = %+v err=%v", loaded, err)
	}
	if nilSnapshot.Version() != "" || nilSnapshot.TTL() != 0 {
		t.Fatal("nil snapshot metadata must use zero values")
	}
	if _, ok := nilSnapshot.Entry("x"); ok {
		t.Fatal("nil snapshot entry must be absent")
	}
}

func TestMutableSnapshotReplaceAndConcurrentReads(t *testing.T) {
	catalog := toolcatalog.NewMutableSnapshot("v1", time.Second, []toolcatalog.Entry{{Name: "one"}})
	if catalog.Version() != "v1" || catalog.TTL() != time.Second {
		t.Fatalf("unexpected initial metadata: %q %s", catalog.Version(), catalog.TTL())
	}
	if hits := catalog.Search("one", 1); len(hits) != 1 {
		t.Fatalf("initial search = %+v", hits)
	}
	catalog.Replace("v2", 2*time.Second, []toolcatalog.Entry{{Name: "two", Tags: []string{"next"}}})
	if catalog.Version() != "v2" || catalog.TTL() != 2*time.Second {
		t.Fatalf("replacement metadata: %q %s", catalog.Version(), catalog.TTL())
	}
	if loaded, err := catalog.Load([]string{"two"}); err != nil || len(loaded) != 1 {
		t.Fatalf("replacement load = %+v err=%v", loaded, err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if index%2 == 0 {
				catalog.Replace("concurrent", time.Second, []toolcatalog.Entry{{Name: "tool"}})
				return
			}
			_ = catalog.Search("tool", 1)
			_, _ = catalog.Load([]string{"tool"})
		}(i)
	}
	wg.Wait()
}
