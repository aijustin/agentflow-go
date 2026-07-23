package tier

import (
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

// ChatMessage is the wire format the runtime persists for one chat message
// inside a tier record's Content field. It intentionally mirrors the
// runtime's internal message schema field-for-field so records seeded by a
// host (e.g. chat transcript hydration) are indistinguishable from records
// the framework writes itself on recall.
type ChatMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []llm.ToolCall    `json:"tool_calls,omitempty"`
	Tool       string            `json:"tool,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Time       time.Time         `json:"time"`
}

// SeedOption customizes MessageRecord.
type SeedOption func(*seedConfig)

type seedConfig struct {
	provenance string
}

// WithProvenance tags the seeded message with a provenance marker (see
// memory.ProvenanceRunTurn, memory.ProvenanceIntegrator) recorded inside the
// message metadata, exactly how the runtime tags its own writes. It does not
// affect recall scoring.
func WithProvenance(provenance string) SeedOption {
	return func(cfg *seedConfig) {
		cfg.provenance = provenance
	}
}

// MessageRecord builds a tier.Record for one chat message using the same
// field-population rules the runtime applies to its own memory writes:
// Content carries the marshaled message, Categories/Importance derive from
// the role, and the record metadata marks role/kind/searchable. Hosts
// hydrating chat history into a tier store should seed through this helper
// (with WithProvenance(memory.ProvenanceIntegrator)) instead of
// re-implementing the mapping, so framework-side recall treats seeded and
// runtime-written records identically.
func MessageRecord(ns memory.Namespace, msg ChatMessage, opts ...SeedOption) (Record, error) {
	cfg := seedConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.provenance != "" {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string)
		}
		msg.Metadata[memory.ProvenanceKey] = cfg.provenance
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return Record{}, err
	}
	id, err := newRecordID()
	if err != nil {
		return Record{}, err
	}
	return Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID:         id,
			Content:    string(raw),
			Scope:      string(ns.Scope),
			Categories: []string{msg.Role},
			Importance: memory.ImportanceForRole(msg.Role),
			CreatedAt:  msg.Time,
			Metadata: map[string]string{
				"role":       msg.Role,
				"kind":       "message",
				"searchable": msg.Content,
			},
		},
		Tier:         LevelHot,
		LastAccessAt: msg.Time,
	}, nil
}
