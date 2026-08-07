package toolorch

import (
	"encoding/json"
	"testing"
)

func TestMemoryApprovalStoreRunStateExportImport(t *testing.T) {
	var store ApprovalStore = NewMemoryApprovalStore()
	exporter, ok := store.(RunStateExporter)
	if !ok {
		t.Fatal("MemoryApprovalStore must implement RunStateExporter")
	}
	// Empty run exports nothing worth persisting.
	if raw, ok := exporter.ExportRun("run-x"); ok || len(raw) != 0 {
		t.Fatalf("empty export = (%s, %v), want (nil, false)", raw, ok)
	}
	keyAllow := Key("risky", json.RawMessage(`{"q":"a"}`))
	keyDeny := Key("risky", json.RawMessage(`{"q":"b"}`))
	store.Put("run-x", keyAllow, DecisionAllow)
	store.Put("run-x", keyDeny, DecisionDeny)
	store.Put("run-x", Key("risky", json.RawMessage(`{"q":"c"}`)), DecisionPause) // not durable
	store.Put("run-other", Key("echo", nil), DecisionAllow)

	raw, ok := exporter.ExportRun("run-x")
	if !ok {
		t.Fatal("expected a non-empty export")
	}
	// Import into a fresh store (a different node's process-local state).
	fresh := NewMemoryApprovalStore()
	if err := fresh.ImportRun("run-x", raw); err != nil {
		t.Fatal(err)
	}
	if decision, ok := fresh.Get("run-x", keyAllow); !ok || decision != DecisionAllow {
		t.Fatalf("allow decision not restored: %q, %v", decision, ok)
	}
	if decision, ok := fresh.Get("run-x", keyDeny); !ok || decision != DecisionDeny {
		t.Fatalf("deny decision not restored: %q, %v", decision, ok)
	}
	var decoded map[ApprovalKey]Decision
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("export must skip non-durable decisions, got %d entries", len(decoded))
	}
	// The export is run-scoped.
	if _, ok := fresh.Get("run-other", Key("echo", nil)); ok {
		t.Fatal("import must not leak decisions across runs")
	}
	// Import replaces (checkpoint is the durable truth): decisions made after
	// the checkpoint on the importing store are dropped.
	fresh.Put("run-x", Key("stale", nil), DecisionAllow)
	if err := fresh.ImportRun("run-x", raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.Get("run-x", Key("stale", nil)); ok {
		t.Fatal("import must replace the run's decisions, not merge")
	}
	if err := fresh.ImportRun("run-x", json.RawMessage(`{`)); err == nil {
		t.Fatal("corrupt export must fail import")
	}
}

func TestDenyBreakerRunStateExportImport(t *testing.T) {
	breaker := NewDenyBreaker(3)
	if got := breaker.ExportRun("run-x"); got != 0 {
		t.Fatalf("empty export = %d, want 0", got)
	}
	breaker.RecordDeny("run-x")
	breaker.RecordDeny("run-x")
	if got := breaker.ExportRun("run-x"); got != 2 {
		t.Fatalf("export = %d, want 2", got)
	}
	// Restore onto a fresh breaker (a different node) and continue the streak.
	fresh := NewDenyBreaker(3)
	fresh.ImportRun("run-x", breaker.ExportRun("run-x"))
	tripped, count := fresh.RecordDeny("run-x")
	if !tripped || count != 3 {
		t.Fatalf("restored breaker must trip at the limit: tripped=%v count=%d", tripped, count)
	}
	// Import replaces in-process state; a non-positive count clears.
	fresh.ImportRun("run-x", 0)
	if got := fresh.ExportRun("run-x"); got != 0 {
		t.Fatalf("clearing import = %d, want 0", got)
	}
}
