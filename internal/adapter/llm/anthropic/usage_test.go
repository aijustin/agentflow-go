package anthropic

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func llmUsage(input, output, cacheRead, cacheWrite int) llm.TokenUsage {
	return llm.TokenUsage{
		InputTokens:       input,
		OutputTokens:      output,
		CachedInputTokens: cacheRead,
		CacheWriteTokens:  cacheWrite,
		TotalTokens:       input + output,
	}
}

// Anthropic reports cache reads and writes alongside input_tokens, so
// input_tokens alone is only the uncached remainder. The adapter has to
// reassemble the full prompt or every cache hit rate computed downstream is
// wrong.
func TestUsagePayloadReassemblesFullPrompt(t *testing.T) {
	payload := usagePayload{
		InputTokens:              12,
		OutputTokens:             40,
		CacheReadInputTokens:     7800,
		CacheCreationInputTokens: 64,
	}
	usage := payload.tokenUsage()

	if usage.InputTokens != 12+7800+64 {
		t.Fatalf("expected the whole prompt in InputTokens, got %d", usage.InputTokens)
	}
	if usage.CachedInputTokens != 7800 {
		t.Fatalf("expected cache reads reported as cached, got %d", usage.CachedInputTokens)
	}
	if usage.CacheWriteTokens != 64 {
		t.Fatalf("expected cache creation reported as writes, got %d", usage.CacheWriteTokens)
	}
	if usage.TotalTokens != 12+7800+64+40 {
		t.Fatalf("unexpected total %d", usage.TotalTokens)
	}
	if rate := usage.CacheHitRate(); rate < 0.98 {
		t.Fatalf("expected a near-total hit rate, got %v", rate)
	}
}

func TestUsagePayloadWithoutCaching(t *testing.T) {
	usage := usagePayload{InputTokens: 500, OutputTokens: 20}.tokenUsage()
	if usage.InputTokens != 500 || usage.TotalTokens != 520 {
		t.Fatalf("unexpected usage %+v", usage)
	}
	if usage.CachedInputTokens != 0 || usage.CacheWriteTokens != 0 {
		t.Fatalf("expected no cache accounting, got %+v", usage)
	}
	if usage.CacheHitRate() != 0 {
		t.Fatalf("expected a zero hit rate, got %v", usage.CacheHitRate())
	}
}

// message_start carries usage under "message"; message_delta carries it at the
// top level. Both must decode.
func TestDecodeStreamEventReadsUsageFromBothShapes(t *testing.T) {
	start, err := decodeStreamEvent([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":990}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if start.Usage.InputTokens != 1000 || start.Usage.CachedInputTokens != 990 {
		t.Fatalf("unexpected message_start usage %+v", start.Usage)
	}

	delta, err := decodeStreamEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}`))
	if err != nil {
		t.Fatal(err)
	}
	if delta.Usage.OutputTokens != 25 {
		t.Fatalf("unexpected message_delta usage %+v", delta.Usage)
	}
}

// The stream merges usage across events; a later output-only event must not
// erase the cache counts reported at the start.
func TestMergeTokenUsageKeepsCacheCounts(t *testing.T) {
	current := llmUsage(1000, 0, 990, 10)
	merged := mergeTokenUsage(current, llmUsage(0, 25, 0, 0))
	if merged.CachedInputTokens != 990 || merged.CacheWriteTokens != 10 {
		t.Fatalf("cache counts lost across merge: %+v", merged)
	}
	if merged.InputTokens != 1000 || merged.OutputTokens != 25 {
		t.Fatalf("unexpected merged usage %+v", merged)
	}
}
