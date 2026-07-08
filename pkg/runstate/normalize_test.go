package runstate

import (
	"encoding/json"
	"testing"
)

func TestNormalizeSnapshotAfterJSONRoundTrip(t *testing.T) {
	raw := []byte(`{"run_id":"run-1","version":1,"scenario_name":"demo","status":"running"}`)
	var snapshot RunSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Variables != nil || snapshot.StepOutputs != nil {
		t.Fatal("expected nil maps before normalize")
	}
	NormalizeSnapshot(&snapshot)
	if snapshot.Variables == nil {
		t.Fatal("expected non-nil Variables")
	}
	if snapshot.StepOutputs == nil {
		t.Fatal("expected non-nil StepOutputs")
	}
	snapshot.StepOutputs["final"] = StepOutputRef{Inline: json.RawMessage(`{"text":"ok"}`)}
}

func TestLoadAuthorizedNormalizesMaps(t *testing.T) {
	repo := stubRepo{snapshot: RunSnapshot{
		RunID:        "run-1",
		ScenarioName: "demo",
		Status:       RunStatusRunning,
	}}
	loaded, err := LoadAuthorized(t.Context(), repo, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Variables == nil || loaded.StepOutputs == nil {
		t.Fatal("LoadAuthorized should normalize nil maps")
	}
}
