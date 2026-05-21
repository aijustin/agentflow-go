package rerank

import (
	"context"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

// ScoreReranker reorders results by normalized score and lexical overlap with the query.
type ScoreReranker struct{}

func NewScoreReranker() *ScoreReranker {
	return &ScoreReranker{}
}

func (r *ScoreReranker) Rerank(_ context.Context, query string, results []knowledge.SearchResult) ([]knowledge.SearchResult, error) {
	if len(results) <= 1 {
		return results, nil
	}
	queryTerms := tokenSet(query)
	type scored struct {
		result knowledge.SearchResult
		score  float64
	}
	ranked := make([]scored, 0, len(results))
	for _, result := range results {
		overlap := lexicalOverlap(queryTerms, tokenSet(result.Document.Content))
		score := result.Score*0.7 + overlap*0.3
		ranked = append(ranked, scored{result: result, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].result.Document.ID < ranked[j].result.Document.ID
		}
		return ranked[i].score > ranked[j].score
	})
	out := make([]knowledge.SearchResult, 0, len(ranked))
	for _, item := range ranked {
		item.result.Score = item.score
		out = append(out, item.result)
	}
	return out, nil
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
