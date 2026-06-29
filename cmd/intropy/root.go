package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	verboseFlag bool
	quietFlag   bool
	noColorFlag bool
)

var rootCmd = &cobra.Command{
	Use:           "intropy",
	Short:         "Intropy CLI",
	Long:          "intropy is the command-line interface for working with Intropy integrations.",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(rootCmd.ErrOrStderr(), "error:", err)
		return err
	}
	return nil
}

// validateOutputFlag returns a usageError if the given format is not in the
// allowed set. Use this for --output flags that accept a fixed list of formats.
func validateOutputFlag(format string, allowed ...string) error {
	for _, a := range allowed {
		if format == a {
			return nil
		}
	}
	return newUsageErrorf("invalid output format %q (allowed: %v)", format, allowed)
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress non-error output")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "disable colored output")
}
