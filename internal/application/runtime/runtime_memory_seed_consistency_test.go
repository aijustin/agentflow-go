package runtime

import (
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

// TestSeedMessageRecordMatchesRuntimeWrite guards the contract that a
// host-seeded record (tier.MessageRecord) is field-for-field identical to a
// runtime-written one (messageToTierRecord), so recall scoring never treats
// hydrated history differently from framework-written history.
func TestSeedMessageRecordMatchesRuntimeWrite(t *testing.T) {
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "sess-1", Agent: "assistant"}
	ts := time.Date(2026, 7, 2, 9, 30, 0, 0, time.UTC)
	msg := memoryMessage{
		Role:    string(llm.RoleUser),
		Content: "hydrate me",
		Metadata: map[string]string{
			"provenance": memory.ProvenanceIntegrator,
			"tier":       "conversation",
		},
		Time: ts,
	}
	runtimeRecord, err := messageToTierRecord(msg, ns)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := tier.MessageRecord(ns, tier.ChatMessage{
		Role:    msg.Role,
		Content: msg.Content,
		Metadata: map[string]string{
			"provenance": memory.ProvenanceIntegrator,
			"tier":       "conversation",
		},
		Time: ts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Content != runtimeRecord.Content {
		t.Fatalf("wire content differs:\nseeded:  %s\nruntime: %s", seeded.Content, runtimeRecord.Content)
	}
	if seeded.Scope != runtimeRecord.Scope ||
		seeded.Importance != runtimeRecord.Importance ||
		seeded.Tier != runtimeRecord.Tier ||
		!seeded.CreatedAt.Equal(runtimeRecord.CreatedAt) ||
		!seeded.LastAccessAt.Equal(runtimeRecord.LastAccessAt) {
		t.Fatalf("record fields differ:\nseeded:  %+v\nruntime: %+v", seeded, runtimeRecord)
	}
	if len(seeded.Categories) != len(runtimeRecord.Categories) ||
		(len(seeded.Categories) > 0 && seeded.Categories[0] != runtimeRecord.Categories[0]) {
		t.Fatalf("categories differ: %v vs %v", seeded.Categories, runtimeRecord.Categories)
	}
	for key, want := range runtimeRecord.Metadata {
		if got := seeded.Metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
}
