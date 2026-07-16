package core

import "context"

// EpisodeCorrelation identifies a platform Episode (one QA test run) that may
// span multiple Runs or HITL resumes. thread_id remains reserved for Fork.
type EpisodeCorrelation struct {
	EpisodeID   string
	TriggerKind string
	SessionID   string
}

type episodeCorrelationKey struct{}

// ContextWithEpisodeCorrelation attaches episode/session correlation used by
// lifecycle event emission and EventStore indexing.
func ContextWithEpisodeCorrelation(ctx context.Context, corr EpisodeCorrelation) context.Context {
	if corr.EpisodeID == "" && corr.TriggerKind == "" && corr.SessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, episodeCorrelationKey{}, corr)
}

// EpisodeCorrelationFromContext returns correlation previously attached with
// ContextWithEpisodeCorrelation, or a zero value when unset.
func EpisodeCorrelationFromContext(ctx context.Context) EpisodeCorrelation {
	corr, _ := ctx.Value(episodeCorrelationKey{}).(EpisodeCorrelation)
	return corr
}

// Empty reports whether no correlation fields are set.
func (c EpisodeCorrelation) Empty() bool {
	return c.EpisodeID == "" && c.TriggerKind == "" && c.SessionID == ""
}
