package main

import "github.com/spf13/cobra"

var manifestsCmd = &cobra.Command{
	Use:   "manifests",
	Short: "Manage deployment manifests",
	Long:  "Generate and manage Kubernetes deployment manifests for scaffolded integrations.",
}

func init() {
	rootCmd.AddCommand(manifestsCmd)
}
