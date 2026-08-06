package toolcatalog

import (
	"testing"
	"time"
)

func TestDeferralPolicyShouldDefer(t *testing.T) {
	cases := []struct {
		name     string
		policy   DeferralPolicy
		size     int
		overhead int
		want     bool
	}{
		{"small catalog below default min", DeferralPolicy{}, 3, 0, false},
		{"at default min defers", DeferralPolicy{}, DefaultDeferralMinTools, 0, true},
		{"above default min defers", DeferralPolicy{}, 20, 0, true},
		{"explicit min respected", DeferralPolicy{MinTools: 2}, 2, 0, true},
		{"below explicit min", DeferralPolicy{MinTools: 2}, 1, 0, false},
		{"negative min normalizes to default", DeferralPolicy{MinTools: -1}, 5, 0, false},
		{"small catalog with heavy overhead defers", DeferralPolicy{MaxOverheadTokens: 100}, 3, 150, true},
		{"overhead within budget advertises", DeferralPolicy{MaxOverheadTokens: 100}, 3, 100, false},
		{"overhead ignored at min size", DeferralPolicy{MaxOverheadTokens: 100}, 8, 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.ShouldDefer(tc.size, tc.overhead); got != tc.want {
				t.Fatalf("ShouldDefer(size=%d, overhead=%d)=%v want %v", tc.size, tc.overhead, got, tc.want)
			}
		})
	}
}

func TestSnapshotDeferralConfig(t *testing.T) {
	entries := []Entry{{Name: "a"}, {Name: "b"}}
	legacy := NewSnapshot("v1", time.Minute, entries)
	if _, ok := legacy.DeferralConfig(); ok {
		t.Fatal("legacy snapshot must not report a deferral policy")
	}
	if got := legacy.Size(); got != 2 {
		t.Fatalf("Size()=%d want 2", got)
	}
	withPolicy := NewSnapshotWithDeferral("v1", time.Minute, entries, DeferralPolicy{MinTools: 4})
	policy, ok := withPolicy.DeferralConfig()
	if !ok {
		t.Fatal("expected configured deferral policy")
	}
	if policy.MinTools != 4 {
		t.Fatalf("MinTools=%d want 4", policy.MinTools)
	}
}

func TestSnapshotOverheadTokens(t *testing.T) {
	empty := NewSnapshot("v1", time.Minute, nil)
	if got := empty.OverheadTokens(); got != 0 {
		t.Fatalf("empty OverheadTokens()=%d want 0", got)
	}
	entry := Entry{Name: "docs.search", Description: "Search the documentation index"}
	snap := NewSnapshot("v1", time.Minute, []Entry{entry})
	want := (len(entry.Name) + len(entry.Description)) / charsPerTokenEstimate
	if got := snap.OverheadTokens(); got != want {
		t.Fatalf("OverheadTokens()=%d want %d", got, want)
	}
}

func TestMutableSnapshotReplacePreservesDeferralPolicy(t *testing.T) {
	mutable := NewMutableSnapshotWithDeferral("v1", time.Minute, []Entry{{Name: "a"}}, DeferralPolicy{MinTools: 3})
	mutable.Replace("v2", time.Minute, []Entry{{Name: "a"}, {Name: "b"}})
	policy, ok := mutable.DeferralConfig()
	if !ok {
		t.Fatal("Replace dropped the deferral policy")
	}
	if policy.MinTools != 3 {
		t.Fatalf("MinTools=%d want 3", policy.MinTools)
	}
	if got := mutable.Size(); got != 2 {
		t.Fatalf("Size()=%d want 2 after Replace", got)
	}
	if mutable.Version() != "v2" {
		t.Fatalf("Version()=%q want v2", mutable.Version())
	}
}
