package main

import "github.com/spf13/cobra"

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Intropy skills",
	Long:  "Manage Intropy skills — install new skills and list installed ones.",
}

func init() {
	rootCmd.AddCommand(skillsCmd)
}
