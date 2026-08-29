package main

import "github.com/spf13/cobra"

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage customer contexts",
	Long:  "Manage the customer contexts in the user configuration file. Contexts themselves are authored in the file by hand; these commands switch, list, and inspect them.",
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
