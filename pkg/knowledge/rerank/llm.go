package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// LLMReranker asks an LLM to rank candidate document IDs for a query.
type LLMReranker struct {
	gateway llm.Gateway
	profile string
}

func NewLLMReranker(gateway llm.Gateway, profile string) *LLMReranker {
	return &LLMReranker{gateway: gateway, profile: profile}
}

func (r *LLMReranker) Rerank(ctx context.Context, query string, results []knowledge.SearchResult) ([]knowledge.SearchResult, error) {
	if r == nil || r.gateway == nil || len(results) <= 1 {
		return results, nil
	}
	candidates := make([]map[string]string, 0, len(results))
	index := make(map[string]knowledge.SearchResult, len(results))
	for _, result := range results {
		index[result.Document.ID] = result
		candidates = append(candidates, map[string]string{
			"id":      result.Document.ID,
			"content": compact(result.Document.Content, 400),
		})
	}
	payload, err := json.Marshal(candidates)
	if err != nil {
		return results, err
	}
	prompt := fmt.Sprintf("Rank document ids by relevance to the query. Return JSON {\"ids\":[\"...\"]}\nQuery: %s\nCandidates: %s", query, string(payload))
	resp, err := r.gateway.Chat(ctx, r.profile, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Return only JSON with an ids array."},
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return results, err
	}
	var ranked struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Message.Content)), &ranked); err != nil {
		fallback := NewScoreReranker()
		return fallback.Rerank(ctx, query, results)
	}
	out := make([]knowledge.SearchResult, 0, len(ranked.IDs))
	seen := make(map[string]struct{}, len(ranked.IDs))
	for rank, id := range ranked.IDs {
		result, ok := index[id]
		if !ok {
			continue
		}
		result.Score = float64(len(ranked.IDs)-rank) / float64(len(ranked.IDs))
		out = append(out, result)
		seen[id] = struct{}{}
	}
	for _, result := range results {
		if _, ok := seen[result.Document.ID]; ok {
			continue
		}
		out = append(out, result)
	}
	return out, nil
}

func compact(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
