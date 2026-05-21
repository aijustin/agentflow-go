package memory

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// CognitiveRecord is a scored long-term memory entry.
type CognitiveRecord struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Scope      string            `json:"scope"`
	Categories []string          `json:"categories,omitempty"`
	Importance float64           `json:"importance"`
	CreatedAt  time.Time         `json:"created_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// CognitiveMemory stores and recalls scored memories for agents.
type CognitiveMemory interface {
	Remember(ctx context.Context, ns Namespace, record CognitiveRecord) error
	Recall(ctx context.Context, ns Namespace, query string, limit int) ([]CognitiveRecord, error)
}

// RecallScore ranks a memory for composite recall scoring.
type RecallScore struct {
	Record CognitiveRecord
	Score  float64
}

// RankMemories applies semantic overlap, recency, and importance weighting.
func RankMemories(query string, records []CognitiveRecord, now time.Time, semanticWeight, recencyWeight, importanceWeight float64) []RecallScore {
	queryTerms := tokenSet(query)
	ranked := make([]RecallScore, 0, len(records))
	for _, record := range records {
		semantic := lexicalOverlap(queryTerms, tokenSet(record.Content))
		ageHours := now.Sub(record.CreatedAt).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		recency := math.Exp(-ageHours / 168.0)
		importance := record.Importance
		if importance <= 0 {
			importance = 0.5
		}
		score := semanticWeight*semantic + recencyWeight*recency + importanceWeight*importance
		ranked = append(ranked, RecallScore{Record: record, Score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Record.ID < ranked[j].Record.ID
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked
}

func tokenSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, token := range strings.Fields(strings.ToLower(text)) {
		if len(token) < 2 {
			continue
		}
		set[token] = struct{}{}
	}
	return set
}

func lexicalOverlap(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	matches := 0
	for token := range a {
		if _, ok := b[token]; ok {
			matches++
		}
	}
	denom := len(a)
	if len(b) > denom {
		denom = len(b)
	}
	return float64(matches) / float64(denom)
}

// EncodeRecord serializes a cognitive record for repository storage.
func EncodeRecord(record CognitiveRecord) (json.RawMessage, error) {
	return json.Marshal(record)
}

// DecodeRecord deserializes a cognitive record from repository storage.
func DecodeRecord(raw json.RawMessage) (CognitiveRecord, error) {
	var record CognitiveRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return CognitiveRecord{}, err
	}
	return record, nil
}
