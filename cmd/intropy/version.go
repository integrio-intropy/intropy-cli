package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "intropy %s (commit: %s, built: %s)\n", version, commit, date)
		if info, ok := debug.ReadBuildInfo(); ok {
			fmt.Fprintf(cmd.OutOrStdout(), "go: %s\n", info.GoVersion)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
