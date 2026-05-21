package main

import (
	"fmt"

	"github.com/spf13/cobra"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
)

func newValidateCommand() *cobra.Command {
	var file, tokenSecret string
	var checkWiring bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a scenario YAML file",
		Long: `Validate scenario YAML structure and references.

Use --wiring to also verify that agentctl run-style demo wiring covers every
declared tool, memory backend, and HITL requirement.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if checkWiring {
				if err := validateScenarioWiring(file, tokenSecret); err != nil {
					return err
				}
			} else if _, err := configyaml.LoadFile(file); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "scenario YAML file")
	cmd.Flags().BoolVar(&checkWiring, "wiring", false, "also validate demo wiring (tools, memory, HITL) like agentctl run")
	cmd.Flags().StringVar(&tokenSecret, "token-secret", "dev-secret", "HITL token secret used during wiring validation")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
