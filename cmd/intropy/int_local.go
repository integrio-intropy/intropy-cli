package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// int local is retired: deploy init grew a local mode, and one pipeline owns
// both destinations now. The stub stays for one release so muscle memory gets
// a pointer rather than "unknown command"; remove it next release.
var intLocalCmd = &cobra.Command{
	Use:    "local <system>",
	Short:  "Render a system's manifests for the local development cluster",
	Hidden: true,
	Args:   cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("int local is replaced by deploy init --local\nuse 'intropy deploy init --local <system> | kubectl apply -f -'")
	},
}

func init() {
	intCmd.AddCommand(intLocalCmd)
}
