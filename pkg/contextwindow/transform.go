package contextwindow

import (
	"bytes"
	"encoding/json"
	"sort"
	"unicode/utf8"
)

// TransformMeta describes how a tool output was reshaped before LLM/memory use.
type TransformMeta struct {
	Truncated      bool   `json:"truncated"`
	OriginalBytes  int    `json:"original_bytes"`
	TruncatedBytes int    `json:"truncated_bytes"`
	Strategy       string `json:"strategy"` // integrator | json_aware | byte_cut | none
}

// ToolOutputTransform reshapes a tool's raw output to fit a byte/token budget.
// limit is a token budget (same units as ToolResultMaxTokens); implementations
// may convert via rune heuristics.
type ToolOutputTransform func(tool string, raw []byte, limit int) (out []byte, meta TransformMeta)

const (
	TransformStrategyIntegrator = "integrator"
	TransformStrategyJSONAware  = "json_aware"
	TransformStrategyByteCut    = "byte_cut"
	TransformStrategyNone       = "none"
)

// ApplyToolOutputTransform applies a per-tool integrator transform when present,
// otherwise JSON-aware truncation for JSON payloads, otherwise byte truncation.
// When limit <= 0 the input is returned unchanged.
func ApplyToolOutputTransform(tool string, raw []byte, limit int, transforms map[string]ToolOutputTransform) ([]byte, TransformMeta) {
	meta := TransformMeta{OriginalBytes: len(raw), TruncatedBytes: len(raw), Strategy: TransformStrategyNone}
	if limit <= 0 || len(raw) == 0 {
		return raw, meta
	}
	if transforms != nil {
		if fn := transforms[tool]; fn != nil {
			out, custom := fn(tool, raw, limit)
			if custom.Strategy == "" {
				custom.Strategy = TransformStrategyIntegrator
			}
			if custom.OriginalBytes == 0 {
				custom.OriginalBytes = len(raw)
			}
			if custom.TruncatedBytes == 0 {
				custom.TruncatedBytes = len(out)
			}
			return out, custom
		}
	}
	if EstimateTokens(string(raw)) <= limit {
		return raw, meta
	}
	if looksLikeJSON(raw) {
		return JSONAwareTruncate(raw, limit)
	}
	return ByteTruncate(raw, limit)
}

// JSONAwareTruncate shortens JSON while keeping it parseable: top-level object
// keys are preserved; long strings and array elements are trimmed.
func JSONAwareTruncate(raw []byte, limit int) ([]byte, TransformMeta) {
	meta := TransformMeta{OriginalBytes: len(raw), Strategy: TransformStrategyJSONAware, Truncated: true}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ByteTruncate(raw, limit)
	}
	budget := limit
	for attempt := 0; attempt < 12; attempt++ {
		shrunk := shrinkJSONValue(value, budget)
		out, err := json.Marshal(shrunk)
		if err != nil {
			return ByteTruncate(raw, limit)
		}
		if EstimateTokens(string(out)) <= limit || budget <= 1 {
			meta.TruncatedBytes = len(out)
			if bytes.Equal(out, raw) {
				meta.Truncated = false
				meta.Strategy = TransformStrategyNone
			}
			return out, meta
		}
		budget /= 2
		if budget < 1 {
			budget = 1
		}
	}
	fallback := []byte(`{"truncated":true}`)
	meta.TruncatedBytes = len(fallback)
	return fallback, meta
}

// ByteTruncate cuts raw bytes on a rune boundary and marks truncation. The
// cut point is chosen against EstimateTokens (not a fixed runes-per-token
// ratio) so CJK-heavy payloads honor the same budget as Latin text.
func ByteTruncate(raw []byte, limit int) ([]byte, TransformMeta) {
	meta := TransformMeta{OriginalBytes: len(raw), Strategy: TransformStrategyByteCut, Truncated: true}
	if limit <= 0 {
		meta.TruncatedBytes = len(raw)
		meta.Truncated = false
		meta.Strategy = TransformStrategyNone
		return raw, meta
	}
	if EstimateTokens(string(raw)) <= limit {
		meta.TruncatedBytes = len(raw)
		meta.Truncated = false
		meta.Strategy = TransformStrategyNone
		return raw, meta
	}
	runes := []rune(string(raw))
	// Start optimistic for Latin text (~3 runes/token), then halve until the
	// estimator accepts the cut — CJK text lands near limit runes.
	runeLimit := limit * 3
	if runeLimit > len(runes) {
		runeLimit = len(runes)
	}
	out := string(runes[:runeLimit]) + "..."
	for EstimateTokens(out) > limit && runeLimit > 1 {
		runeLimit /= 2
		out = string(runes[:runeLimit]) + "..."
	}
	meta.TruncatedBytes = len(out)
	return []byte(out), meta
}

func looksLikeJSON(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{', '[':
		return json.Valid(trimmed)
	default:
		return false
	}
}

func shrinkJSONValue(value any, budget int) any {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			out[key] = shrinkJSONValue(v[key], budget)
		}
		if _, ok := out["truncated"]; !ok {
			out["truncated"] = true
		}
		return out
	case []any:
		if len(v) == 0 {
			return v
		}
		keep := len(v)
		if budget > 0 && keep > budget {
			keep = budget
		}
		if keep < 1 {
			keep = 1
		}
		if keep > len(v) {
			keep = len(v)
		}
		out := make([]any, 0, keep)
		for i := 0; i < keep; i++ {
			out = append(out, shrinkJSONValue(v[i], budget))
		}
		return out
	case string:
		return truncateRunes(v, budget*3)
	case json.Number:
		return v
	default:
		return v
	}
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}
