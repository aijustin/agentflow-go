package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// TestFrameworkReaperDiscardsUnconfirmedPauseCheckpoint pins the D7 reaper
// behavior: a Running run that crashed between the checkpoint write and
// gate.Pause carries the pending-pause marker; its checkpoint was never
// approved, so reaping must discard it (a later RetryFailedRun must not
// execute unapproved state). A confirmed checkpoint (marker cleared by the
// resume path) survives reaping and stays resumable.
func TestFrameworkReaperDiscardsUnconfirmedPauseCheckpoint(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "reaper", time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	zombie := func(runID string, pendingPause bool) runstate.RunSnapshot {
		vars := map[string]json.RawMessage{
			runstate.VarRunLeaseOwner:  json.RawMessage(`"dead-worker"`),
			runstate.VarCheckpointKind: json.RawMessage(`"before_final_answer"`),
			"checkpoint_prompt":        json.RawMessage(`"hi"`),
			"checkpoint_agent":         json.RawMessage(`"assistant"`),
		}
		if pendingPause {
			vars[runstate.VarCheckpointPendingPause] = json.RawMessage(`true`)
		}
		return runstate.RunSnapshot{RunID: runID, ScenarioName: "wf-retry", Status: runstate.RunStatusRunning, Variables: vars}
	}
	unconfirmed := zombie("run-zombie-unconfirmed", true)
	if err := repo.Save(context.Background(), &unconfirmed, 0); err != nil {
		t.Fatal(err)
	}
	confirmed := zombie("run-zombie-confirmed", false)
	if err := repo.Save(context.Background(), &confirmed, 0); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)
	marked, err := fw.MarkAbandonedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 2 {
		t.Fatalf("expected both zombies reaped, got %v", marked)
	}

	got, err := repo.Load(context.Background(), "run-zombie-unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if _, ok := got.Variables[runstate.VarCheckpointKind]; ok {
		t.Fatalf("unconfirmed checkpoint must be discarded, got %s", got.Variables[runstate.VarCheckpointKind])
	}
	if _, ok := got.Variables[runstate.VarCheckpointPendingPause]; ok {
		t.Fatal("pending-pause marker must be discarded with the checkpoint")
	}
	if msg := string(got.Variables[runstate.VarRunErrorMessage]); !strings.Contains(msg, "unconfirmed pause checkpoint discarded") {
		t.Fatalf("expected discard reason on snapshot, got %s", msg)
	}

	kept, err := repo.Load(context.Background(), "run-zombie-confirmed")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed, got %s", kept.Status)
	}
	if got := string(kept.Variables[runstate.VarCheckpointKind]); got != `"before_final_answer"` {
		t.Fatalf("confirmed checkpoint must survive reaping for RetryFailedRun, got %s", got)
	}
}

// TestFrameworkContinueRunRefusesUnconfirmedPauseCheckpoint pins the D7
// fail-closed guard at the framework recovery entry point: knowing the run ID
// must not be enough to execute a checkpoint whose pause was never confirmed.
func TestFrameworkContinueRunRefusesUnconfirmedPauseCheckpoint(t *testing.T) {
	fw := newHITLFramework(t, "should never run")
	repo := fw.RunStateRepository()
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-unconfirmed", ScenarioName: "human-in-loop-demo", Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			runstate.VarCheckpointKind:         json.RawMessage(`"before_final_answer"`),
			"checkpoint_prompt":                json.RawMessage(`"hi"`),
			"checkpoint_agent":                 json.RawMessage(`"assistant"`),
			runstate.VarCheckpointPendingPause: json.RawMessage(`true`),
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	_, err := fw.ContinueRun(context.Background(), "run-unconfirmed")
	if err == nil || !strings.Contains(err.Error(), "unconfirmed pause") {
		t.Fatalf("expected unconfirmed-pause refusal, got %v", err)
	}
	snapshot, err := repo.Load(context.Background(), "run-unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning {
		t.Fatalf("refused continue must not change run status, got %s", snapshot.Status)
	}
}

// TestFrameworkContinueRunWithToken pins the D7 credential check: the tokened
// recovery entry point rejects missing, malformed, foreign, and expired
// tokens, and drives the approved run to completion with the genuine pause
// token.
func TestFrameworkContinueRunWithToken(t *testing.T) {
	fw := newHITLFramework(t, "token answer")
	paused := runUntilPaused(t, fw, "run-tokened-recovery")
	// Approve without continuing: the zombie state ContinueRun exists for.
	if err := fw.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}

	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, err := signer.Sign(runstate.TokenPayload{RunID: "run-other", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	expiredToken, err := signer.Sign(runstate.TokenPayload{
		RunID:     "run-tokened-recovery",
		Version:   1,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"missing token", "", runstate.ErrInvalidToken},
		{"malformed token", "not-a-token", runstate.ErrInvalidToken},
		{"foreign run token", foreignToken, runstate.ErrInvalidToken},
		{"expired token", expiredToken, runstate.ErrTokenExpired},
	}
	for _, tc := range cases {
		if _, err := fw.ContinueRunWithToken(context.Background(), "run-tokened-recovery", tc.token); !errors.Is(err, tc.want) {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-tokened-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning {
		t.Fatalf("rejected tokens must not advance the run, got %s", snapshot.Status)
	}

	result, err := fw.ContinueRunWithToken(context.Background(), "run-tokened-recovery", paused.Token)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed result, got %+v", result)
	}
	if !strings.Contains(result.Output, "token answer") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}
