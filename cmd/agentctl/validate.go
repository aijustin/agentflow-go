package main

import (
	"fmt"

	"github.com/spf13/cobra"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
)

func newValidateCommand() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := configyaml.LoadFile(file); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "scenario YAML file")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
