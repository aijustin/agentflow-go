package main

import "github.com/spf13/cobra"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentctl",
		Short:         "Run scenario-configured Go agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newValidateCommand())
	cmd.AddCommand(newSchemaCommand())
	cmd.AddCommand(newRunCommand())
	cmd.AddCommand(newResumeCommand())
	cmd.AddCommand(newTriggerCommand())
	cmd.AddCommand(newVersionCommand())
	return cmd
}
