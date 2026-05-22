package memory

import "strings"

// RecallWeights configures semantic, recency, and importance weighting for RankMemories.
type RecallWeights struct {
	Semantic   float64 `json:"semantic,omitempty"`
	Recency    float64 `json:"recency,omitempty"`
	Importance float64 `json:"importance,omitempty"`
}

// DefaultRecallWeights returns the default RankMemories weighting.
func DefaultRecallWeights() RecallWeights {
	return RecallWeights{Semantic: 0.5, Recency: 0.3, Importance: 0.2}
}

// Normalize fills zero values with defaults.
func (w RecallWeights) Normalize() RecallWeights {
	def := DefaultRecallWeights()
	if w.Semantic <= 0 {
		w.Semantic = def.Semantic
	}
	if w.Recency <= 0 {
		w.Recency = def.Recency
	}
	if w.Importance <= 0 {
		w.Importance = def.Importance
	}
	return w
}

// ImportanceForRole returns a default cognitive importance score for a message role.
func ImportanceForRole(role string) float64 {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return 0.7
	case "assistant":
		return 0.6
	case "tool":
		return 0.4
	default:
		return 0.5
	}
}

// SearchableContent returns plain text suitable for cognitive recall indexing.
// When content is JSON (tier message envelope), callers should pass decoded message text.
func SearchableContent(content string) string {
	return strings.TrimSpace(content)
}
