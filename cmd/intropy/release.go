package main

import (
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Publish and inspect immutable release manifests",
	Long: "A release names a set of built bits: a component version, the source commit it was built from, and the " +
		"image digests CI published for that commit.\n\n" +
		"Releases change no environment. Creating one records what a version means; deploying one is a separate step.",
}

// completeReleaseComponents suggests component names from the cached GitOps
// checkout. Like every completion in this package it stays silent on error and
// never touches the network — a shell completion must not print a diagnostic.
func completeReleaseComponents(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// --gitops-repo belongs to whichever subcommand is completing, so read it
	// from there rather than from one subcommand's flag globals.
	var gitopsRepo string
	if f := cmd.Flags().Lookup("gitops-repo"); f != nil {
		gitopsRepo = f.Value.String()
	}
	root, err := gitops.CachedRoot(gitopsRepo)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var names []string
	for _, c := range gitops.ListComponents(root) {
		if !seen[c.Component] {
			seen[c.Component] = true
			names = append(names, c.Component)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeReleaseOutput(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{release.OutputPlain, release.OutputJSON}, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	rootCmd.AddCommand(releaseCmd)
}
