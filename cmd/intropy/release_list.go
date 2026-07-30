package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

// release list prints the versions published for a component. Fetching and
// rendering live in internal/release.List; this file is flag plumbing.
type releaseListFlags struct {
	domain     string
	system     string
	gitopsRepo string
	output     string
}

var releaseListOpts = releaseListFlags{output: release.OutputPlain}

var releaseListCmd = &cobra.Command{
	Use:   "list <component>",
	Short: "List the releases published for a component",
	Long: "List the versions released for a component, newest first, with the date each was cut, its source commit, and the " +
		"first line of its notes. Pass one of those versions to release show to read the full manifest.\n\n" +
		"Releases are read from the registry beside the component's images, so this reports what is actually published rather " +
		"than what git tags claim. It changes no source repository, GitOps remote, or environment.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(releaseListOpts.output, release.OutputPlain, release.OutputJSON); err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return release.List(ctx, release.Options{
			Component:    args[0],
			Domain:       releaseListOpts.domain,
			System:       releaseListOpts.system,
			GitopsRepo:   releaseListOpts.gitopsRepo,
			OutputFormat: releaseListOpts.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := releaseListCmd.Flags()
	f.StringVar(&releaseListOpts.domain, "domain", "", flagUsageDomain)
	f.StringVar(&releaseListOpts.system, "system", "", flagUsageSystem)
	f.StringVar(&releaseListOpts.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.StringVarP(&releaseListOpts.output, "output", "o", release.OutputPlain, flagUsageOutput)

	releaseCmd.AddCommand(releaseListCmd)
}
