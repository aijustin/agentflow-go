package main

import (
	"encoding/json"
	"time"

	"github.com/spf13/cobra"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func newResumeCommand() *cobra.Command {
	var token, decisionText, amendment, tokenSecret, stateDir string
	var tokenTTL time.Duration
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused run from a human decision token",
		RunE: func(cmd *cobra.Command, args []string) error {
			var decision core.Decision
			if err := decision.UnmarshalText([]byte(decisionText)); err != nil {
				return err
			}
			signer, err := runstate.NewTokenSigner([]byte(tokenSecret))
			if err != nil {
				return err
			}
			repo, err := newRunRepository(stateDir)
			if err != nil {
				return err
			}
			gate := humancli.NewGate(repo, signer, cmd.OutOrStdout(), humancli.WithTokenTTL(tokenTTL))
			return gate.Resume(cmd.Context(), token, decision, json.RawMessage(amendment))
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "resume token")
	cmd.Flags().StringVar(&decisionText, "decision", "", "approve, reject, or amend")
	cmd.Flags().StringVar(&amendment, "amendment", "", "optional amendment JSON")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HMAC token secret")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "directory for durable run state")
	cmd.Flags().DurationVar(&tokenTTL, "token-ttl", 15*time.Minute, "human approval token lifetime")
	_ = cmd.MarkFlagRequired("token")
	_ = cmd.MarkFlagRequired("decision")
	return cmd
}
