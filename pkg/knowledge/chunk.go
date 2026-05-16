package knowledge

import (
	"fmt"
	"strconv"
)

const (
	DefaultChunkMaxRunes     = 1200
	DefaultChunkOverlapRunes = 120
)

type Chunker interface {
	Split(document Document) ([]Document, error)
}

type TextSplitterConfig struct {
	MaxRunes     int
	OverlapRunes int
}

type TextSplitter struct {
	maxRunes     int
	overlapRunes int
}

func NewTextSplitter(config TextSplitterConfig) (*TextSplitter, error) {
	maxRunes := config.MaxRunes
	if maxRunes <= 0 {
		maxRunes = DefaultChunkMaxRunes
	}
	overlapRunes := config.OverlapRunes
	if config.MaxRunes <= 0 && overlapRunes == 0 {
		overlapRunes = DefaultChunkOverlapRunes
	}
	if overlapRunes < 0 {
		return nil, fmt.Errorf("knowledge splitter: overlap must be >= 0")
	}
	if overlapRunes >= maxRunes {
		return nil, fmt.Errorf("knowledge splitter: overlap must be smaller than max runes")
	}
	return &TextSplitter{maxRunes: maxRunes, overlapRunes: overlapRunes}, nil
}

func (s *TextSplitter) Split(document Document) ([]Document, error) {
	if document.ID == "" {
		return nil, fmt.Errorf("knowledge splitter: document id is required")
	}
	runes := []rune(document.Content)
	if len(runes) == 0 {
		return nil, nil
	}
	ranges := make([]chunkRange, 0, (len(runes)/s.maxRunes)+1)
	for start := 0; start < len(runes); {
		end := start + s.maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		ranges = append(ranges, chunkRange{start: start, end: end})
		if end == len(runes) {
			break
		}
		next := end - s.overlapRunes
		if next <= start {
			next = end
		}
		start = next
	}
	chunks := make([]Document, 0, len(ranges))
	for index, item := range ranges {
		metadata := cloneMetadata(document.Metadata)
		metadata["parent_id"] = document.ID
		metadata["chunk_index"] = strconv.Itoa(index)
		metadata["chunk_count"] = strconv.Itoa(len(ranges))
		metadata["chunk_start"] = strconv.Itoa(item.start)
		metadata["chunk_end"] = strconv.Itoa(item.end)
		chunks = append(chunks, Document{
			ID:        fmt.Sprintf("%s#chunk-%06d", document.ID, index+1),
			Content:   string(runes[item.start:item.end]),
			Metadata:  metadata,
			Namespace: document.Namespace,
		})
	}
	return chunks, nil
}

type chunkRange struct {
	start int
	end   int
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+5)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
