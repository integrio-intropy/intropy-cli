package main

import (
	"github.com/spf13/cobra"
)

var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Publish and inspect immutable release manifests",
	Long: "A release names a set of built bits: a component version, the source commit it was built from, and the " +
		"image digests CI published for that commit.\n\n" +
		"Releases change no environment. Creating one records what a version means; deploying one is a separate step.",
}

func init() {
	rootCmd.AddCommand(releaseCmd)
}
