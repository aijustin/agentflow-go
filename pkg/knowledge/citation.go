package knowledge

type Citation struct {
	ID         string `json:"id"`
	ParentID   string `json:"parent_id,omitempty"`
	Source     string `json:"source,omitempty"`
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	ChunkIndex string `json:"chunk_index,omitempty"`
	ChunkStart string `json:"chunk_start,omitempty"`
	ChunkEnd   string `json:"chunk_end,omitempty"`
}

func CitationFromDocument(document Document) Citation {
	metadata := document.Metadata
	parentID := firstNonEmpty(metadata["parent_id"], document.ID)
	return Citation{
		ID:         document.ID,
		ParentID:   parentID,
		Source:     firstNonEmpty(metadata["source"], metadata["path"]),
		Title:      metadata["title"],
		URL:        metadata["url"],
		ChunkIndex: metadata["chunk_index"],
		ChunkStart: metadata["chunk_start"],
		ChunkEnd:   metadata["chunk_end"],
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
