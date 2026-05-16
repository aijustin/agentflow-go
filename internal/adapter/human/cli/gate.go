package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type Gate struct {
	repo     runstate.Repository
	signer   *runstate.TokenSigner
	out      io.Writer
	tokenTTL time.Duration
}

type GateOption func(*Gate)

func WithTokenTTL(ttl time.Duration) GateOption {
	return func(g *Gate) {
		if ttl > 0 {
			g.tokenTTL = ttl
		}
	}
}

func NewGate(repo runstate.Repository, signer *runstate.TokenSigner, out io.Writer, opts ...GateOption) *Gate {
	gate := &Gate{repo: repo, signer: signer, out: out}
	for _, opt := range opts {
		if opt != nil {
			opt(gate)
		}
	}
	return gate
}

func (g *Gate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	snapshot, err := g.repo.Load(ctx, state.RunID)
	if err != nil {
		return "", err
	}
	if snapshot.Version != state.Version {
		return "", runstate.ErrStaleSnapshot
	}
	if !snapshot.Status.CanTransitionTo(runstate.RunStatusPaused) {
		return "", runstate.ErrInvalidTransition
	}
	snapshot.Status = runstate.RunStatusPaused
	snapshot.PendingGate = &state
	if err := g.repo.Save(ctx, &snapshot, state.Version); err != nil {
		return "", err
	}
	payload := runstate.TokenPayload{RunID: state.RunID, Version: snapshot.Version}
	if g.tokenTTL > 0 {
		payload.ExpiresAt = time.Now().UTC().Add(g.tokenTTL)
	}
	token, err := g.signer.Sign(payload)
	if err != nil {
		return "", err
	}
	if g.out != nil {
		_, _ = fmt.Fprintln(g.out, token)
	}
	return token, nil
}

func (g *Gate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	if !decision.Valid() {
		return fmt.Errorf("human cli: invalid decision %q", decision)
	}
	payload, err := g.signer.Verify(token)
	if err != nil {
		return err
	}
	snapshot, err := g.repo.Load(ctx, payload.RunID)
	if err != nil {
		return err
	}
	if snapshot.Version != payload.Version {
		return runstate.ErrTokenSuperseded
	}
	if snapshot.Status != runstate.RunStatusPaused {
		return runstate.ErrInvalidTransition
	}
	switch decision {
	case core.DecisionApprove, core.DecisionAmend:
		snapshot.Status = runstate.RunStatusRunning
	case core.DecisionReject:
		snapshot.Status = runstate.RunStatusCancelled
	}
	snapshot.PendingGate = nil
	if len(amendment) > 0 {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables["human_amendment"] = amendment
	}
	return g.repo.Save(ctx, &snapshot, payload.Version)
}
