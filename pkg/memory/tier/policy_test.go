package tier

import (
	"testing"
	"time"
)

func TestPolicyShouldPromote(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now().UTC()

	warm := Record{Tier: LevelWarm, AccessCount: 3, LastAccessAt: now}
	if !p.ShouldPromote(warm, now) {
		t.Fatal("warm record with enough accesses should promote")
	}

	hot := Record{Tier: LevelHot, AccessCount: 10, LastAccessAt: now}
	if p.ShouldPromote(hot, now) {
		t.Fatal("hot record should not promote further")
	}

	pinned := Record{Tier: LevelWarm, AccessCount: 10, Pinned: true}
	if p.ShouldPromote(pinned, now) {
		t.Fatal("pinned record should not promote")
	}
}

func TestPolicyShouldDemoteHotCapacity(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now().UTC()
	record := Record{Tier: LevelHot, LastAccessAt: now}
	if !p.ShouldDemote(record, now, p.HotCapacity+1) {
		t.Fatal("expected hot demotion when capacity exceeded")
	}
}

func TestPolicyShouldDemoteIdle(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now().UTC()
	record := Record{
		Tier:         LevelWarm,
		LastAccessAt: now.Add(-8 * 24 * time.Hour),
	}
	if !p.ShouldDemote(record, now, 1) {
		t.Fatal("expected warm demotion when idle")
	}
}

func TestTargetTierPromoteWarmToHot(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now().UTC()
	record := Record{Tier: LevelWarm, AccessCount: 5, LastAccessAt: now}
	got := p.TargetTier(record, now, map[Level]int{LevelWarm: 1})
	if got != LevelHot {
		t.Fatalf("got %q, want hot", got)
	}
}

func TestRecallBudgetNormalize(t *testing.T) {
	b := RecallBudget{Total: 10}.Normalize()
	if b.Hot+b.Warm+b.Cold != 10 {
		t.Fatalf("budget should sum to total, got hot=%d warm=%d cold=%d", b.Hot, b.Warm, b.Cold)
	}
}

func TestNextTierOnDemote(t *testing.T) {
	next, ok := DefaultPolicy().NextTierOnDemote(LevelHot)
	if !ok || next != LevelWarm {
		t.Fatalf("hot demotion = %q ok=%v", next, ok)
	}
	if _, ok := DefaultPolicy().NextTierOnDemote(LevelCold); ok {
		t.Fatal("cold should require eviction, not demotion")
	}
}
