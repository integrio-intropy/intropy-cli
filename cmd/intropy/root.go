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
	Long:          "intropy is the command-line interface for working with Intropy skills and integrations.",
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

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress non-error output")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "disable colored output")
}
