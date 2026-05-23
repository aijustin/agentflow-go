package tier

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

const defaultColdSummaryMinBytes = 4096

const defaultColdSummaryMaxChars = 512

// ColdSummarySettings configures cold-tier summarization before archive.
type ColdSummarySettings struct {
	Enabled         bool   `json:"enabled,omitempty" yaml:"enabled"`
	MinBytes        int64  `json:"min_bytes,omitempty" yaml:"min_bytes"`
	MaxSummaryChars int    `json:"max_summary_chars,omitempty" yaml:"max_summary_chars"`
	SummaryProfile  string `json:"summary_profile,omitempty" yaml:"summary_profile"`
}

// ContentSummarizer compresses large record content before cold archive.
type ContentSummarizer interface {
	Summarize(ctx context.Context, content string, maxChars int) (string, error)
}

// ColdSummaryBackend archives cold records and optionally indexes summaries for semantic recall.
type ColdSummaryBackend interface {
	Archive(ctx context.Context, ns memory.Namespace, record *Record) error
	SearchRecordIDs(ctx context.Context, ns memory.Namespace, query string, limit int) ([]string, error)
	Delete(ctx context.Context, ns memory.Namespace, recordID string) error
}

// NoopColdSummaryBackend disables cold summary behavior.
type NoopColdSummaryBackend struct{}

func (NoopColdSummaryBackend) Archive(context.Context, memory.Namespace, *Record) error { return nil }
func (NoopColdSummaryBackend) SearchRecordIDs(context.Context, memory.Namespace, string, int) ([]string, error) {
	return nil, nil
}
func (NoopColdSummaryBackend) Delete(context.Context, memory.Namespace, string) error { return nil }

// TruncateColdSummaryBackend summarizes large records with a deterministic truncate fallback.
type TruncateColdSummaryBackend struct {
	Settings   ColdSummarySettings
	Vector     ColdSummaryIndexer
	Summarizer ContentSummarizer
}

// ColdSummaryIndexer stores and searches cold summaries in a vector index.
type ColdSummaryIndexer interface {
	UpsertSummary(ctx context.Context, ns memory.Namespace, record Record, summary string) error
	SearchSummaries(ctx context.Context, ns memory.Namespace, query string, limit int) ([]string, error)
	DeleteSummary(ctx context.Context, ns memory.Namespace, recordID string) error
}

func (b TruncateColdSummaryBackend) Archive(ctx context.Context, ns memory.Namespace, record *Record) error {
	if !b.Settings.Enabled || record == nil {
		return nil
	}
	settings := b.Settings.normalized()
	size := recordSize(*record)
	if size < settings.MinBytes {
		return nil
	}
	source := extractSearchable(*record)
	summary := truncateSummary(source, settings.MaxSummaryChars)
	if b.Summarizer != nil {
		if llmSummary, err := b.Summarizer.Summarize(ctx, source, settings.MaxSummaryChars); err == nil {
			if trimmed := strings.TrimSpace(llmSummary); trimmed != "" {
				summary = truncateSummary(trimmed, settings.MaxSummaryChars)
			}
		}
	}
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	record.Metadata["searchable"] = summary
	record.Metadata["cold_summary"] = "true"
	record.Content = summary
	record.SizeBytes = int64(len(summary))
	if b.Vector != nil {
		return b.Vector.UpsertSummary(ctx, ns, *record, summary)
	}
	return nil
}

func (b TruncateColdSummaryBackend) SearchRecordIDs(ctx context.Context, ns memory.Namespace, query string, limit int) ([]string, error) {
	if !b.Settings.Enabled || b.Vector == nil || limit <= 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	return b.Vector.SearchSummaries(ctx, ns, query, limit)
}

func (b TruncateColdSummaryBackend) Delete(ctx context.Context, ns memory.Namespace, recordID string) error {
	if b.Vector == nil {
		return nil
	}
	return b.Vector.DeleteSummary(ctx, ns, recordID)
}

func (s ColdSummarySettings) normalized() ColdSummarySettings {
	out := s
	if out.MinBytes <= 0 {
		out.MinBytes = defaultColdSummaryMinBytes
	}
	if out.MaxSummaryChars <= 0 {
		out.MaxSummaryChars = defaultColdSummaryMaxChars
	}
	return out
}

func recordSize(record Record) int64 {
	if record.SizeBytes > 0 {
		return record.SizeBytes
	}
	return int64(len(record.Content))
}

func truncateSummary(content string, maxChars int) string {
	content = strings.TrimSpace(content)
	if maxChars <= 0 {
		maxChars = defaultColdSummaryMaxChars
	}
	if utf8.RuneCountInString(content) <= maxChars {
		return content
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	return string(runes[:maxChars]) + "…"
}
