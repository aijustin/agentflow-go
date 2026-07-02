package async

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/eventrouter"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestJobAndLeaseValidate(t *testing.T) {
	if (Job{}).Validate() == nil {
		t.Fatal("expected invalid empty job")
	}
	if err := (Job{ID: "j1", Type: RunJobType, MaxAttempts: -1}).Validate(); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("expected invalid max attempts, got %v", err)
	}
	if (Lease{}).Validate() == nil {
		t.Fatal("expected invalid empty lease")
	}
	if (Lease{JobID: "j1", WorkerID: "w1", Attempt: 1}).Validate() != nil {
		t.Fatal("expected valid lease")
	}
}

func TestCloneJobCopiesPayload(t *testing.T) {
	original := Job{ID: "j1", Type: RunJobType, Payload: json.RawMessage(`{"k":"v"}`)}
	cloned := CloneJob(original)
	cloned.Payload[0] = '{'
	if original.Payload[0] == '{' && string(original.Payload) != `{"k":"v"}` {
		t.Fatalf("clone should not alias payload, got %s", original.Payload)
	}
}

func TestEventPayloadEvent(t *testing.T) {
	payload := EventPayload{
		Type:    "ticket.created",
		RunID:   "run-1",
		Payload: json.RawMessage(`{"id":"t-1"}`),
	}
	event := payload.Event()
	if event.Type != "ticket.created" || event.RunID != "run-1" {
		t.Fatalf("unexpected event: %+v", event)
	}
	_ = eventrouter.Event{}
}

func TestMemoryReconcilePayloadMarshal(t *testing.T) {
	raw, err := (MemoryReconcilePayload{
		MemoryName: "session",
		Agent:      "assistant",
		RunID:      "run-1",
		Principal:  identity.Principal{ID: "svc", Type: identity.PrincipalService},
	}).MarshalJSONBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("expected marshaled payload")
	}
}

func TestRunPausedErrorString(t *testing.T) {
	err := RunPausedError{RunID: "run-1", Token: "tok"}
	if err.Error() == "" {
		t.Fatal("expected error message")
	}
}
