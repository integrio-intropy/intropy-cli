package main

import "github.com/spf13/cobra"

var manifestsCmd = &cobra.Command{
	Use:   "manifests",
	Short: "Inspect, render, and create Kubernetes manifests",
	Long: "Inspect the deployment model derived from a system topology, render complete YAML for a local " +
		"cluster, or create missing files on a GitOps review branch.",
}

func init() {
	rootCmd.AddCommand(manifestsCmd)
}
