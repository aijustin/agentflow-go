package knowledge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MergeStrategy controls how MultiNamespaceRetriever combines per-namespace hits.
type MergeStrategy string

const (
	// MergeStrategyGlobalRank sorts all namespace hits by score (default).
	MergeStrategyGlobalRank MergeStrategy = "global_rank"
	// MergeStrategyBalanced interleaves results so each namespace contributes evenly.
	MergeStrategyBalanced MergeStrategy = "balanced"
)

// MultiNamespaceOption configures MultiNamespaceRetriever.
type MultiNamespaceOption func(*MultiNamespaceRetriever)

// WithMergeStrategy sets how results from multiple namespaces are merged.
func WithMergeStrategy(strategy MergeStrategy) MultiNamespaceOption {
	return func(r *MultiNamespaceRetriever) {
		r.strategy = strategy
	}
}

// MultiNamespaceRetriever fans a Query out across namespaces and merges hits.
type MultiNamespaceRetriever struct {
	store      VectorStore
	namespaces []string
	strategy   MergeStrategy
}

// NewMultiNamespaceRetriever wraps store.Query with multi-namespace fan-out.
func NewMultiNamespaceRetriever(store VectorStore, namespaces []string, opts ...MultiNamespaceOption) (*MultiNamespaceRetriever, error) {
	if store == nil {
		return nil, fmt.Errorf("knowledge: multi-namespace retriever store is nil")
	}
	if len(namespaces) == 0 {
		return nil, fmt.Errorf("knowledge: multi-namespace retriever requires at least one namespace")
	}
	cloned := append([]string(nil), namespaces...)
	retriever := &MultiNamespaceRetriever{
		store:      store,
		namespaces: cloned,
		strategy:   MergeStrategyGlobalRank,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(retriever)
		}
	}
	if retriever.strategy == "" {
		retriever.strategy = MergeStrategyGlobalRank
	}
	switch retriever.strategy {
	case MergeStrategyGlobalRank, MergeStrategyBalanced:
	default:
		return nil, fmt.Errorf("knowledge: unknown merge strategy %q", retriever.strategy)
	}
	return retriever, nil
}

// Query runs the query against each configured namespace and merges results.
// A single namespace failure is skipped; if every namespace fails, the joined error is returned.
func (r *MultiNamespaceRetriever) Query(ctx context.Context, query Query) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}

	type nsResult struct {
		namespace string
		results   []SearchResult
		err       error
	}
	out := make([]nsResult, len(r.namespaces))
	var wg sync.WaitGroup
	for i, namespace := range r.namespaces {
		wg.Add(1)
		go func(index int, ns string) {
			defer wg.Done()
			nsQuery := query
			nsQuery.Namespace = ns
			results, err := r.store.Query(ctx, nsQuery)
			if err != nil {
				out[index] = nsResult{namespace: ns, err: err}
				return
			}
			annotated := make([]SearchResult, 0, len(results))
			for _, result := range results {
				annotated = append(annotated, injectNamespaceMetadata(result, ns))
			}
			out[index] = nsResult{namespace: ns, results: annotated}
		}(i, namespace)
	}
	wg.Wait()

	lists := make([][]SearchResult, 0, len(out))
	var errs []error
	for _, item := range out {
		if item.err != nil {
			errs = append(errs, fmt.Errorf("namespace %q: %w", item.namespace, item.err))
			continue
		}
		lists = append(lists, item.results)
	}
	if len(lists) == 0 {
		if len(errs) == 0 {
			return nil, fmt.Errorf("knowledge: multi-namespace query produced no results")
		}
		return nil, fmt.Errorf("knowledge: all namespaces failed: %w", errors.Join(errs...))
	}

	switch r.strategy {
	case MergeStrategyBalanced:
		return mergeBalanced(lists, limit), nil
	default:
		return mergeGlobalRank(lists, limit), nil
	}
}

func injectNamespaceMetadata(result SearchResult, namespace string) SearchResult {
	meta := make(map[string]string, len(result.Document.Metadata)+2)
	for key, value := range result.Document.Metadata {
		meta[key] = value
	}
	meta["namespace"] = namespace
	if meta["kb_id"] == "" {
		meta["kb_id"] = namespace
	}
	result.Document.Namespace = namespace
	result.Document.Metadata = meta
	return result
}

func mergeGlobalRank(lists [][]SearchResult, limit int) []SearchResult {
	total := 0
	for _, list := range lists {
		total += len(list)
	}
	merged := make([]SearchResult, 0, total)
	seen := make(map[string]struct{}, total)
	for _, list := range lists {
		for _, result := range list {
			key := resultKey(result)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, result)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			return resultKey(merged[i]) < resultKey(merged[j])
		}
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func mergeBalanced(lists [][]SearchResult, limit int) []SearchResult {
	if limit <= 0 {
		return nil
	}
	cursors := make([]int, len(lists))
	seen := make(map[string]struct{}, limit)
	out := make([]SearchResult, 0, limit)
	for len(out) < limit {
		progress := false
		for i, list := range lists {
			for cursors[i] < len(list) {
				result := list[cursors[i]]
				cursors[i]++
				key := resultKey(result)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, result)
				progress = true
				break
			}
			if len(out) >= limit {
				break
			}
		}
		if !progress {
			break
		}
	}
	return out
}

func resultKey(result SearchResult) string {
	ns := result.Document.Namespace
	id := result.Document.ID
	if id == "" {
		return ""
	}
	return ns + "\x00" + id
}
