package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/eventrouter"
)

func newTriggerCommand() *cobra.Command {
	var file, eventType, runID, payload, tokenSecret, stateDir string
	var tokenTTL time.Duration
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "trigger",
		Short: "Dispatch an external event against a scenario",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenWriter := cmd.OutOrStdout()
			if outputJSON {
				tokenWriter = io.Discard
			}
			fw, err := newFrameworkFromFlags(file, stateDir, tokenSecret, tokenTTL, tokenWriter)
			if err != nil {
				return err
			}
			event := eventrouter.Event{
				Type:    eventType,
				RunID:   runID,
				Payload: json.RawMessage(payload),
			}
			result, err := fw.HandleEvent(cmd.Context(), event)
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
	cmd.Flags().StringVar(&eventType, "event", "", "event type configured in scenario.triggers")
	cmd.Flags().StringVar(&runID, "run-id", "", "optional run id override")
	cmd.Flags().StringVar(&payload, "payload", "{}", "event payload JSON")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HMAC token secret")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "directory for durable run state")
	cmd.Flags().DurationVar(&tokenTTL, "token-ttl", 15*time.Minute, "human approval token lifetime")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "write machine-readable JSON output")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("event")
	return cmd
}

func formatCLIResult(result appexec.RunResult) string {
	out := fmt.Sprintf("run_id=%s status=%s", result.RunID, result.Status)
	if result.Token != "" {
		out += fmt.Sprintf(" token=%s", result.Token)
	}
	if result.Output != "" {
		out += fmt.Sprintf("\n%s", result.Output)
	}
	return out
}
