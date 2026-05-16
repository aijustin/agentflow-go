package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestGatePausePersistsSnapshotAndPrintsToken(t *testing.T) {
	repo, signer, out := newGateDeps(t)
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	gate := NewGate(repo, signer, out)

	token, err := gate.Pause(context.Background(), core.CheckpointState{RunID: "run-1", Version: snapshot.Version, NodeID: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || !strings.Contains(out.String(), token) {
		t.Fatalf("expected printed token, token=%q out=%q", token, out.String())
	}
	loaded, err := repo.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusPaused || loaded.PendingGate == nil {
		t.Fatalf("expected paused snapshot with gate, got %+v", loaded)
	}
}

func TestGatePauseSignsExpiringTokenWhenTTLConfigured(t *testing.T) {
	repo, signer, out := newGateDeps(t)
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	gate := NewGate(repo, signer, out, WithTokenTTL(time.Minute))

	token, err := gate.Pause(context.Background(), core.CheckpointState{RunID: "run-1", Version: snapshot.Version, NodeID: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := signer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ExpiresAt.IsZero() {
		t.Fatalf("expected token expiration")
	}
}

func TestGatePauseRejectsStaleVersion(t *testing.T) {
	repo, signer, out := newGateDeps(t)
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	_, err := NewGate(repo, signer, out).Pause(context.Background(), core.CheckpointState{RunID: "run-1", Version: 99})
	if !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot, got %v", err)
	}
}

func TestGateResumeDecisions(t *testing.T) {
	tests := []struct {
		name       string
		decision   core.Decision
		amendment  json.RawMessage
		wantStatus runstate.RunStatus
		wantAmend  bool
	}{
		{name: "approve resumes", decision: core.DecisionApprove, wantStatus: runstate.RunStatusRunning},
		{name: "amend resumes and stores amendment", decision: core.DecisionAmend, amendment: []byte(`{"fixed":true}`), wantStatus: runstate.RunStatusRunning, wantAmend: true},
		{name: "reject cancels", decision: core.DecisionReject, wantStatus: runstate.RunStatusCancelled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, signer, out := newGateDeps(t)
			snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
			if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
				t.Fatal(err)
			}
			gate := NewGate(repo, signer, out)
			token, err := gate.Pause(context.Background(), core.CheckpointState{RunID: "run-1", Version: snapshot.Version, NodeID: "approval"})
			if err != nil {
				t.Fatal(err)
			}
			if err := gate.Resume(context.Background(), token, tt.decision, tt.amendment); err != nil {
				t.Fatal(err)
			}
			loaded, err := repo.Load(context.Background(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Status != tt.wantStatus {
				t.Fatalf("got status %s, want %s", loaded.Status, tt.wantStatus)
			}
			if tt.wantAmend && string(loaded.Variables["human_amendment"]) != string(tt.amendment) {
				t.Fatalf("amendment not stored: %+v", loaded.Variables)
			}
		})
	}
}

func TestGateResumeRejectsSupersededToken(t *testing.T) {
	repo, signer, out := newGateDeps(t)
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	gate := NewGate(repo, signer, out)
	token, err := gate.Pause(context.Background(), core.CheckpointState{RunID: "run-1", Version: snapshot.Version, NodeID: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), &loaded, loaded.Version); err != nil {
		t.Fatal(err)
	}
	err = gate.Resume(context.Background(), token, core.DecisionApprove, nil)
	if !errors.Is(err, runstate.ErrTokenSuperseded) {
		t.Fatalf("expected token superseded, got %v", err)
	}
}

func newGateDeps(t *testing.T) (*runstateinmem.Repository, *runstate.TokenSigner, *bytes.Buffer) {
	t.Helper()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	return runstateinmem.NewRepository(), signer, &bytes.Buffer{}
}
