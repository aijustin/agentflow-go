package eventrouter

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestRouterResolveTicketCreated(t *testing.T) {
	router := NewRouter(core.Scenario{
		Agents: map[string]core.Agent{"support": {Name: "support"}},
		Triggers: []core.Trigger{{
			Event:       "ticket.created",
			Agent:       "support",
			PromptPath:  "body.summary",
			ContextPath: "body",
			RunIDPath:   "body.ticket_id",
		}},
	})
	req, err := router.Resolve(Event{
		Type: "ticket.created",
		Payload: json.RawMessage(`{
			"body": {
				"ticket_id": "T-42",
				"summary": "Cannot login"
			}
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.RunID != "T-42" || req.Agent != "support" || req.Prompt != "Cannot login" {
		t.Fatalf("unexpected request: %+v", req)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Context, &body); err != nil {
		t.Fatal(err)
	}
	if body["ticket_id"] != "T-42" {
		t.Fatalf("unexpected context: %+v", body)
	}
}

func TestRouterResolveUnknownEvent(t *testing.T) {
	router := NewRouter(core.Scenario{})
	if _, err := router.Resolve(Event{Type: "unknown"}); err == nil {
		t.Fatal("expected unknown event error")
	}
}
