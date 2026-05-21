package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	agentflow "github.com/aijustin/agentflow-go"
)

func newRunCommand() *cobra.Command {
	var file, prompt, runID, tokenSecret, stateDir string
	var tokenTTL time.Duration
	var outputJSON, verbose bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenWriter := cmd.OutOrStdout()
			if outputJSON {
				tokenWriter = io.Discard
			}
			fw, err := newFrameworkFromFlags(file, stateDir, tokenSecret, tokenTTL, tokenWriter, verbose, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			result, err := fw.Run(cmd.Context(), agentflow.RunRequest{RunID: runID, Prompt: prompt})
			if err != nil {
				return err
			}
			if outputJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), formatCLIResult(result))
			return err
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "scenario YAML file")
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt input")
	cmd.Flags().StringVar(&runID, "run-id", "", "run id")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HMAC token secret")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "directory for durable run state and blobs")
	cmd.Flags().DurationVar(&tokenTTL, "token-ttl", 15*time.Minute, "human approval token lifetime")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "write machine-readable JSON output")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "log runtime events to stderr")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
