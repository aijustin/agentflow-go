// Package tier implements the tiered memory subsystem: tier-scoped record
// stores, a manager that remembers and recalls records across hot/warm/cold
// levels, and the policies that migrate records between them.
//
// # Single durable tier (no composite, no reconcile queue)
//
// A host that only needs durable session memory (e.g. the Postgres tier
// store) can back the manager with a single-level store directly — no
// composite and no job queue are required. The store forces its own level
// on Put and answers List/Count for other levels with empty results, so
// Remember persists immediately and Recall reads the durable tier:
//
//	store, _ := postgres.NewStore(db)                 // level = warm
//	manager := tier.NewManager(store, tier.SingleLevelPolicy(), nil)
//
// SingleLevelPolicy disables promotion, demotion, TTL, and capacity
// eviction, so recall-time bookkeeping never migrates records out of the
// durable tier. Hosts seeding chat history should build records with
// MessageRecord (optionally WithProvenance), which populates fields exactly
// like the runtime's own writes.
//
// # Approximating a flat "most recent, replayed in order" recall
//
// A flat memory.Repository replays every message in insertion order and
// consumers typically tail the newest N. RankMemories instead SCORES
// candidates: score = semantic*lexicalOverlap(query) + recency*exp(-age/168h)
// + importance*record.Importance, weighted by RecallWeights (defaults
// {Semantic: 0.5, Recency: 0.3, Importance: 0.2}; zero weights are replaced
// by those defaults, so use small positive values to de-emphasize a
// dimension). RecallBudget then caps the selection (default Total 20, split
// 60%/25%/15% across hot/warm/cold by Normalize). The final output is
// ALWAYS re-sorted chronologically — ranking only picks what survives, not
// the replay order. To approximate the flat tail-N behavior:
//
//   - recall with an empty query (no semantic signal),
//   - set weights recency-dominant, e.g. {Semantic: 0.01, Recency: 1.0,
//     Importance: 0.01},
//   - size RecallBudget.Total at the replay window (e.g. 200) and raise the
//     per-level caps to match.
package tier
