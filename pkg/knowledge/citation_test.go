package knowledge

import "testing"

func TestCitationFromDocumentUsesChunkMetadata(t *testing.T) {
	citation := CitationFromDocument(Document{ID: "doc-1#chunk-000001", Metadata: map[string]string{
		"parent_id":   "doc-1",
		"source":      "guide",
		"title":       "Guide",
		"url":         "https://example.test/guide",
		"chunk_index": "0",
		"chunk_start": "10",
		"chunk_end":   "20",
	}})
	if citation.ID != "doc-1#chunk-000001" || citation.ParentID != "doc-1" || citation.Source != "guide" || citation.Title != "Guide" || citation.URL == "" || citation.ChunkIndex != "0" || citation.ChunkStart != "10" || citation.ChunkEnd != "20" {
		t.Fatalf("unexpected citation: %+v", citation)
	}
}

func TestCitationFromDocumentFallsBackToPathSource(t *testing.T) {
	citation := CitationFromDocument(Document{ID: "doc-1", Metadata: map[string]string{"path": "/tmp/doc-1.md"}})
	if citation.ParentID != "doc-1" || citation.Source != "/tmp/doc-1.md" {
		t.Fatalf("unexpected citation fallback: %+v", citation)
	}
}
