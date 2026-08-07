package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// int local is retired. The stub stays for one release so muscle memory gets
// a pointer rather than "unknown command"; remove it next release.
var intLocalCmd = &cobra.Command{
	Use:    "local <system>",
	Short:  "Render a system's manifests for the local development cluster",
	Hidden: true,
	Args:   cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("int local is replaced by manifests render\nuse 'intropy manifests render --env local | kubectl apply -f -'")
	},
}

func init() {
	intCmd.AddCommand(intLocalCmd)
}
