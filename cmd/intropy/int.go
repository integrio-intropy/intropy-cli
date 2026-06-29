package main

import "github.com/spf13/cobra"

var intCmd = &cobra.Command{
	Use:   "int",
	Short: "Manage integrations",
	Long:  "Manage Intropy integrations — create new ones and run existing ones.",
}

func init() {
	rootCmd.AddCommand(intCmd)
}
