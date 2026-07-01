package agentflow_test

import (
	"context"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// pauseForToken runs a minimal HITL scenario until the before-final-answer
// pause and returns the emitted resume token.
func pauseForToken(t *testing.T, opts ...agentflow.Option) string {
	t.Helper()
	opts = append([]agentflow.Option{agentflow.WithHITLTokenSecret([]byte("secret"), nil)}, opts...)
	fw, err := agentflow.New(builder.MinimalHumanInLoop("assistant"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-token-ttl", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token == "" {
		t.Fatalf("expected paused run with token, got %+v", result)
	}
	return result.Token
}

func TestFrameworkHITLTokenExpiresByDefault(t *testing.T) {
	token := pauseForToken(t)
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := signer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ExpiresAt.IsZero() {
		t.Fatal("default HITL token must carry an expiry")
	}
	remaining := time.Until(payload.ExpiresAt)
	if remaining <= 23*time.Hour || remaining > 24*time.Hour {
		t.Fatalf("expected ~24h default expiry, got %s", remaining)
	}
}

func TestFrameworkHITLTokenTTLZeroDisablesExpiry(t *testing.T) {
	token := pauseForToken(t, agentflow.WithHITLTokenTTL(0))
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := signer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.ExpiresAt.IsZero() {
		t.Fatalf("explicit TTL 0 must issue non-expiring tokens, got expiry %s", payload.ExpiresAt)
	}
}

func TestFrameworkResumeRunByIDTokenCarriesExpiry(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("secret"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "approved"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-resume-ttl", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run, got %+v", result)
	}
	resumed, err := fw.ResumeRunByID(context.Background(), "run-resume-ttl", core.DecisionApprove, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after resume, got %+v", resumed)
	}
}
