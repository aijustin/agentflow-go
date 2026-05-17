package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type Config struct {
	Embedder            llm.Embedder
	Store               knowledge.VectorStore
	Profile             string
	Namespace           string
	DefaultLimit        int
	SearchMode          knowledge.SearchMode
	CandidateMultiplier int
	Reranker            knowledge.Reranker
	VectorWeight        float64
	TextWeight          float64
}

type Tool struct {
	embedder            llm.Embedder
	store               knowledge.VectorStore
	profile             string
	namespace           string
	defaultLimit        int
	searchMode          knowledge.SearchMode
	candidateMultiplier int
	reranker            knowledge.Reranker
	vectorWeight        float64
	textWeight          float64
}

type Request struct {
	Query     string               `json:"query"`
	Namespace string               `json:"namespace,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Filter    map[string]string    `json:"filter,omitempty"`
	Mode      knowledge.SearchMode `json:"mode,omitempty"`
}

type Response struct {
	Results []Result `json:"results"`
}

type Result struct {
	ID       string             `json:"id"`
	Content  string             `json:"content"`
	Score    float64            `json:"score"`
	Metadata map[string]string  `json:"metadata,omitempty"`
	Citation knowledge.Citation `json:"citation"`
}

func NewTool(config Config) (*Tool, error) {
	if config.Embedder == nil {
		return nil, fmt.Errorf("retriever tool: embedder is nil")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("retriever tool: vector store is nil")
	}
	if config.Profile == "" {
		return nil, fmt.Errorf("retriever tool: embedding profile is required")
	}
	limit := config.DefaultLimit
	if limit <= 0 {
		limit = 5
	}
	searchMode := config.SearchMode
	if searchMode == "" {
		searchMode = knowledge.SearchModeVector
	}
	multiplier := config.CandidateMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	return &Tool{embedder: config.Embedder, store: config.Store, profile: config.Profile, namespace: config.Namespace, defaultLimit: limit, searchMode: searchMode, candidateMultiplier: multiplier, reranker: config.Reranker, vectorWeight: config.VectorWeight, textWeight: config.TextWeight}, nil
}

func (tool *Tool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	var req Request
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &req); err != nil {
			return core.ToolResult{}, fmt.Errorf("retriever tool: decode input: %w", err)
		}
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return core.ToolResult{}, fmt.Errorf("retriever tool: query is required")
	}
	vectors, err := tool.embedder.Embed(ctx, tool.profile, []string{req.Query})
	if err != nil {
		return core.ToolResult{}, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return core.ToolResult{}, fmt.Errorf("retriever tool: embedding response is empty")
	}
	namespace := firstNonEmpty(req.Namespace, tool.namespace)
	limit := req.Limit
	if limit <= 0 {
		limit = tool.defaultLimit
	}
	searchMode := req.Mode
	if searchMode == "" {
		searchMode = tool.searchMode
	}
	candidateLimit := limit
	if tool.reranker != nil && tool.candidateMultiplier > 1 {
		candidateLimit = limit * tool.candidateMultiplier
	}
	query := knowledge.Query{Namespace: namespace, Text: req.Query, Mode: searchMode, Vector: vectors[0], Limit: candidateLimit, Filter: req.Filter, VectorWeight: tool.vectorWeight, TextWeight: tool.textWeight}
	results, err := tool.search(ctx, query)
	if err != nil {
		return core.ToolResult{}, err
	}
	if tool.reranker != nil {
		results, err = tool.reranker.Rerank(ctx, req.Query, results)
		if err != nil {
			return core.ToolResult{}, err
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	out := Response{Results: make([]Result, 0, len(results))}
	for _, result := range results {
		out.Results = append(out.Results, Result{ID: result.Document.ID, Content: result.Document.Content, Score: result.Score, Metadata: cloneMetadata(result.Document.Metadata), Citation: knowledge.CitationFromDocument(result.Document)})
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: payload}, nil
}

func (tool *Tool) search(ctx context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	if query.Mode == knowledge.SearchModeHybrid {
		if hybrid, ok := tool.store.(knowledge.HybridSearcher); ok {
			return hybrid.HybridQuery(ctx, query)
		}
	}
	return tool.store.Query(ctx, query)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
