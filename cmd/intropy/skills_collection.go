package main

import "github.com/spf13/cobra"

var skillsCollectionCmd = &cobra.Command{
	Use:   "collection",
	Short: "Manage registered skill collections",
}

func init() {
	skillsCmd.AddCommand(skillsCollectionCmd)
}
