package slog

import (
	"bytes"
	"context"
	"encoding/json"
	stdslog "log/slog"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestSinkRecordsStructuredAuditEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(stdslog.New(stdslog.NewJSONHandler(&buf, nil)))
	if err := sink.Record(context.Background(), audit.Event{
		Type:      audit.EventPolicyDenied,
		Timestamp: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		Principal: identity.Principal{ID: "user-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}},
		Action:    security.ActionRunSubmit,
		Resource:  security.Resource{Type: "run", ID: "run-1", TenantID: "tenant-1"},
		RunID:     "run-1",
		Outcome:   "denied",
		Reason:    "forbidden",
	}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["audit_type"] != string(audit.EventPolicyDenied) || got["principal_id"] != "user-1" || got["tenant_id"] != "tenant-1" || got["action"] != string(security.ActionRunSubmit) || got["resource_id"] != "run-1" {
		t.Fatalf("unexpected log fields: %+v", got)
	}
	if _, ok := got["payload"]; ok {
		t.Fatalf("payload should be omitted by default: %+v", got)
	}
}

func TestSinkCanIncludePayload(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(stdslog.New(stdslog.NewJSONHandler(&buf, nil)), WithPayload())
	if err := sink.Record(context.Background(), audit.Event{Type: audit.EventRunSubmitted, RunID: "run-1", Payload: json.RawMessage(`{"source":"test"}`)}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["payload"] != `{"source":"test"}` {
		t.Fatalf("expected payload field, got %+v", got)
	}
}
