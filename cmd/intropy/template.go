package main

import "github.com/spf13/cobra"

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Inspect the Intropy template library",
	Long:  "List the templates in the Intropy template library and inspect their manifests and parameter schemas.",
}

func init() {
	rootCmd.AddCommand(templateCmd)
}
