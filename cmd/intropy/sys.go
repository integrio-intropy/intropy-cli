package main

import "github.com/spf13/cobra"

var sysCmd = &cobra.Command{
	Use:   "sys",
	Short: "Manage integration systems",
	Long:  "Manage Intropy integration systems — assemble scaffolded integrations into a system host.",
}

func init() {
	rootCmd.AddCommand(sysCmd)
}
