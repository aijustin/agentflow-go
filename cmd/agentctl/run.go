package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	eventstdout "github.com/aijustin/agentflow-go/internal/adapter/event/stdout"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func newRunCommand() *cobra.Command {
	var file, prompt, runID, tokenSecret, stateDir string
	var tokenTTL time.Duration
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			scenario, err := configyaml.LoadFile(file)
			if err != nil {
				return err
			}
			repo, blobs, err := newRunStores(stateDir)
			if err != nil {
				return err
			}
			signer, err := runstate.NewTokenSigner([]byte(tokenSecret))
			if err != nil {
				return err
			}
			tokenWriter := cmd.OutOrStdout()
			if outputJSON {
				tokenWriter = io.Discard
			}
			gate := humancli.NewGate(repo, signer, tokenWriter, humancli.WithTokenTTL(tokenTTL))
			events := eventstdout.NewSink(cmd.ErrOrStderr())
			engine, err := appexec.NewEngine(scenario, appexec.Dependencies{
				Runs:      repo,
				Blobs:     blobs,
				Events:    events,
				HumanGate: gate,
			})
			if err != nil {
				return err
			}
			result, err := engine.Run(cmd.Context(), appexec.RunRequest{RunID: runID, Prompt: prompt})
			if err != nil {
				return err
			}
			if outputJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "run_id=%s status=%s\n", result.RunID, result.Status)
			if result.Output != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "scenario YAML file")
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt input")
	cmd.Flags().StringVar(&runID, "run-id", "", "run id")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HMAC token secret")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "directory for durable run state and blobs")
	cmd.Flags().DurationVar(&tokenTTL, "token-ttl", 15*time.Minute, "human approval token lifetime")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "write machine-readable JSON output")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
