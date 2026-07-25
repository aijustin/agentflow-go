package toolcatalog_test

import (
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
