package knowledge

import "testing"

func TestTextSplitterSplitsDocumentWithOverlapAndCitationMetadata(t *testing.T) {
	splitter, err := NewTextSplitter(TextSplitterConfig{MaxRunes: 10, OverlapRunes: 3})
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := splitter.Split(Document{ID: "doc-1", Namespace: "tenant-a/docs", Content: "abcdefghij1234567890", Metadata: map[string]string{"source": "guide"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %+v", chunks)
	}
	assertChunk(t, chunks[0], "doc-1#chunk-000001", "abcdefghij", "0", "10", "0")
	assertChunk(t, chunks[1], "doc-1#chunk-000002", "hij1234567", "7", "17", "1")
	assertChunk(t, chunks[2], "doc-1#chunk-000003", "567890", "14", "20", "2")
	for _, chunk := range chunks {
		if chunk.Namespace != "tenant-a/docs" || chunk.Metadata["source"] != "guide" || chunk.Metadata["parent_id"] != "doc-1" || chunk.Metadata["chunk_count"] != "3" {
			t.Fatalf("unexpected chunk metadata: %+v", chunk)
		}
	}
}

func TestTextSplitterValidatesConfig(t *testing.T) {
	if _, err := NewTextSplitter(TextSplitterConfig{MaxRunes: 10, OverlapRunes: 10}); err == nil {
		t.Fatal("expected overlap validation error")
	}
}

func assertChunk(t *testing.T, chunk Document, id, content, start, end, index string) {
	t.Helper()
	if chunk.ID != id || chunk.Content != content || chunk.Metadata["chunk_start"] != start || chunk.Metadata["chunk_end"] != end || chunk.Metadata["chunk_index"] != index {
		t.Fatalf("unexpected chunk: %+v", chunk)
	}
}
