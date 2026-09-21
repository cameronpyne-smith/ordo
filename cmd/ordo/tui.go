package main

import (
	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/tui"
)

// runTUI is what bare `ordo` does. The terminal is the surface you sit in
// front of, so it is the one that needs no subcommand.
func runTUI(configPath *string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c, err := newClient(*configPath)
		if err != nil {
			return err
		}
		return tui.Run(c)
	}
}
