package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

var errContinueRequiresScenarioFile = errors.New("agentctl: --continue requires --file")

func newResumeCommand() *cobra.Command {
	var (
		file, token, decisionText, amendment, tokenSecret, stateDir string
		tokenTTL                                                    time.Duration
		continueRun, outputJSON                                     bool
	)
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused run from a human decision token",
		RunE: func(cmd *cobra.Command, args []string) error {
			var decision core.Decision
			if err := decision.UnmarshalText([]byte(decisionText)); err != nil {
				return err
			}
			amendmentJSON := json.RawMessage(amendment)
			if continueRun {
				if file == "" {
					return errContinueRequiresScenarioFile
				}
				tokenWriter := cmd.OutOrStdout()
				if outputJSON {
					tokenWriter = io.Discard
				}
				fw, err := newFrameworkFromFlags(file, stateDir, tokenSecret, tokenTTL, tokenWriter, false, nil)
				if err != nil {
					return err
				}
				result, err := fw.ResumeAndContinue(cmd.Context(), token, decision, amendmentJSON)
				if err != nil {
					return err
				}
				if outputJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
				}
				_, err = fmt.Fprint(cmd.OutOrStdout(), formatCLIResult(result))
				return err
			}
			if file != "" {
				fw, err := newFrameworkFromFlags(file, stateDir, tokenSecret, tokenTTL, cmd.OutOrStdout(), false, nil)
				if err != nil {
					return err
				}
				if err := fw.Resume(cmd.Context(), token, decision, amendmentJSON); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "status=running")
				return nil
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
			return gate.Resume(cmd.Context(), token, decision, amendmentJSON)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "scenario YAML file (required for --continue)")
	cmd.Flags().StringVar(&token, "token", "", "resume token")
	cmd.Flags().StringVar(&decisionText, "decision", "", "approve, reject, or amend")
	cmd.Flags().StringVar(&amendment, "amendment", "", "optional amendment JSON")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HMAC token secret")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "directory for durable run state")
	cmd.Flags().DurationVar(&tokenTTL, "token-ttl", 15*time.Minute, "human approval token lifetime")
	cmd.Flags().BoolVar(&continueRun, "continue", false, "resume and continue execution until completion or next pause")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "write machine-readable JSON output")
	_ = cmd.MarkFlagRequired("token")
	_ = cmd.MarkFlagRequired("decision")
	return cmd
}
