package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aijustin/agentflow-go/schemas"
)

func newSchemaCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the AgentFlow scenario JSON Schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "json":
				_, err := cmd.OutOrStdout().Write(schemas.ScenarioJSONSchema())
				return err
			default:
				return fmt.Errorf("unsupported schema format %q", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "json", "schema format (json)")
	return cmd
}
