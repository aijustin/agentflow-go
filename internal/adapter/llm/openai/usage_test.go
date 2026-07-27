package openai

import "testing"

// OpenAI already reports cached_tokens as a subset of prompt_tokens, which is
// the meaning llm.TokenUsage documents, so the prompt total must be left alone
// rather than having the cached count added to it.
func TestUsagePayloadTreatsCachedTokensAsSubsetOfPrompt(t *testing.T) {
	usage := usagePayload{
		PromptTokens:     8000,
		CompletionTokens: 120,
		TotalTokens:      8120,
	}
	usage.PromptTokensDetails.CachedTokens = 7680
	got := usage.tokenUsage()

	if got.InputTokens != 8000 {
		t.Fatalf("expected the prompt total unchanged, got %d", got.InputTokens)
	}
	if got.CachedInputTokens != 7680 {
		t.Fatalf("expected cached tokens carried through, got %d", got.CachedInputTokens)
	}
	if got.UncachedInputTokens() != 320 {
		t.Fatalf("expected 320 uncached tokens, got %d", got.UncachedInputTokens())
	}
	if rate := got.CacheHitRate(); rate < 0.95 {
		t.Fatalf("expected a high hit rate, got %v", rate)
	}
}

// Some OpenAI-compatible providers use the newer input_tokens naming.
func TestUsagePayloadAcceptsInputTokensNaming(t *testing.T) {
	usage := usagePayload{InputTokens: 500, OutputTokens: 30, TotalTokens: 530}
	usage.InputTokensDetails.CachedTokens = 400
	got := usage.tokenUsage()

	if got.InputTokens != 500 || got.OutputTokens != 30 {
		t.Fatalf("unexpected usage %+v", got)
	}
	if got.CachedInputTokens != 400 {
		t.Fatalf("expected cached tokens from input_tokens_details, got %d", got.CachedInputTokens)
	}
}

func TestUsagePayloadWithoutCacheDetails(t *testing.T) {
	got := usagePayload{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}.tokenUsage()
	if got.CachedInputTokens != 0 || got.CacheHitRate() != 0 {
		t.Fatalf("expected no cache accounting, got %+v", got)
	}
}
