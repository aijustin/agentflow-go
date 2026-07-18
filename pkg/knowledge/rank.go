package knowledge

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// DecayConfig controls temporal score decay for session-like sources.
type DecayConfig struct {
	// HalfLifeDays is the exponential half-life for decaying sources.
	// Zero disables decay.
	HalfLifeDays float64
	// Now overrides the reference time (tests). Zero means time.Now().
	Now time.Time
	// EvergreenSources are metadata "source" values exempt from decay
	// (default: global, workspace).
	EvergreenSources []string
}

// ApplyTemporalDecay multiplies each result score by e^(-λ * age_days) for
// non-evergreen sources. Age is read from metadata keys created_at (unix
// seconds or RFC3339) or updated_at. Results without a parseable timestamp
// are left unchanged.
func ApplyTemporalDecay(results []SearchResult, cfg DecayConfig) []SearchResult {
	if cfg.HalfLifeDays <= 0 || len(results) == 0 {
		return results
	}
	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	evergreen := map[string]struct{}{}
	sources := cfg.EvergreenSources
	if len(sources) == 0 {
		sources = []string{"global", "workspace"}
	}
	for _, source := range sources {
		evergreen[strings.ToLower(strings.TrimSpace(source))] = struct{}{}
	}
	lambda := math.Ln2 / cfg.HalfLifeDays
	out := make([]SearchResult, len(results))
	copy(out, results)
	for i := range out {
		source := strings.ToLower(strings.TrimSpace(out[i].Document.Metadata["source"]))
		if _, ok := evergreen[source]; ok {
			continue
		}
		created, ok := parseResultTime(out[i].Document.Metadata)
		if !ok {
			continue
		}
		ageDays := now.Sub(created).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		out[i].Score = out[i].Score * math.Exp(-lambda*ageDays)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

func parseResultTime(meta map[string]string) (time.Time, bool) {
	if meta == nil {
		return time.Time{}, false
	}
	for _, key := range []string{"created_at", "updated_at", "timestamp"} {
		raw := strings.TrimSpace(meta[key])
		if raw == "" {
			continue
		}
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return time.Unix(unix, 0).UTC(), true
		}
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

// MMRConfig controls Maximal Marginal Relevance re-ranking.
type MMRConfig struct {
	// Lambda balances relevance (1) vs diversity (0). Default 0.7.
	Lambda float64
	// Limit caps the returned list. Zero keeps all results.
	Limit int
}

// ApplyMMR re-ranks results to reduce near-duplicate content while preserving
// high relevance. Similarity is a simple token Jaccard over document content
// (and document id when content is empty).
func ApplyMMR(results []SearchResult, cfg MMRConfig) []SearchResult {
	if len(results) <= 1 {
		return results
	}
	lambda := cfg.Lambda
	if lambda <= 0 || lambda > 1 {
		lambda = 0.7
	}
	limit := cfg.Limit
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	selected := make([]SearchResult, 0, limit)
	remaining := make([]SearchResult, len(results))
	copy(remaining, results)
	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := 0
		bestScore := math.Inf(-1)
		for i, candidate := range remaining {
			relevance := candidate.Score
			maxSim := 0.0
			for _, chosen := range selected {
				sim := contentSimilarity(candidate.Document, chosen.Document)
				if sim > maxSim {
					maxSim = sim
				}
			}
			mmr := lambda*relevance - (1-lambda)*maxSim
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

func contentSimilarity(a, b Document) float64 {
	if a.ID != "" && a.ID == b.ID {
		return 1
	}
	ta := tokenize(a.Content)
	tb := tokenize(b.Content)
	if len(ta) == 0 || len(tb) == 0 {
		if a.Metadata["path"] != "" && a.Metadata["path"] == b.Metadata["path"] {
			return 0.8
		}
		return 0
	}
	intersection := 0
	for token := range ta {
		if _, ok := tb[token]; ok {
			intersection++
		}
	}
	union := len(ta) + len(tb) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenize(text string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) < 2 {
			continue
		}
		out[field] = struct{}{}
	}
	return out
}
