package knowledge

import "sort"

// MergeRRF fuses ranked result lists with reciprocal rank fusion.
func MergeRRF(lists [][]SearchResult, k int, limit int) []SearchResult {
	if k <= 0 {
		k = 60
	}
	if limit <= 0 {
		limit = 5
	}
	scores := make(map[string]float64)
	docs := make(map[string]Document)
	for _, list := range lists {
		for rank, result := range list {
			id := result.Document.ID
			if id == "" {
				continue
			}
			scores[id] += 1.0 / (float64(k) + float64(rank+1))
			if _, ok := docs[id]; !ok {
				docs[id] = result.Document
			}
		}
	}
	type ranked struct {
		id    string
		score float64
	}
	order := make([]ranked, 0, len(scores))
	for id, score := range scores {
		order = append(order, ranked{id: id, score: score})
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].score == order[j].score {
			return order[i].id < order[j].id
		}
		return order[i].score > order[j].score
	})
	if len(order) > limit {
		order = order[:limit]
	}
	out := make([]SearchResult, 0, len(order))
	for _, item := range order {
		out = append(out, SearchResult{Document: docs[item.id], Score: item.score})
	}
	return out
}
