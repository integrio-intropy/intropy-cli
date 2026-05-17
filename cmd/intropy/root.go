package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "intropy",
	Short:         "Intropy CLI",
	Long:          "intropy is the command-line interface for working with Intropy skills and integrations.",
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
