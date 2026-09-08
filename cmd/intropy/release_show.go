package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

// release show prints a published release manifest. Fetching and rendering
// live in internal/release.Show; this file is flag plumbing.
type releaseShowFlags struct {
	domain     string
	system     string
	gitopsRepo string
	output     string
}

var releaseShowOpts = releaseShowFlags{output: release.OutputPlain}

var releaseShowCmd = &cobra.Command{
	Use:   "show <component> <version>",
	Short: "Read a published release manifest",
	Long: "Read the release manifest for a version and print what it records: the source commit, the pinned image " +
		"digests, what the notes were measured against, and the notes themselves.\n\n" +
		"It changes no source repository, GitOps remote, or environment. Use it to sanity-check generated notes before deploying anything from the release.",
	Args: usageArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(releaseShowOpts.output, release.OutputPlain, release.OutputJSON); err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return release.Show(ctx, release.Options{
			Component:    args[0],
			Version:      args[1],
			Domain:       releaseShowOpts.domain,
			System:       releaseShowOpts.system,
			GitopsRepo:   releaseShowOpts.gitopsRepo,
			OutputFormat: releaseShowOpts.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := releaseShowCmd.Flags()
	f.StringVar(&releaseShowOpts.domain, "domain", "", flagUsageDomain)
	f.StringVar(&releaseShowOpts.system, "system", "", flagUsageSystem)
	f.StringVar(&releaseShowOpts.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.StringVarP(&releaseShowOpts.output, "output", "o", release.OutputPlain, flagUsageOutput)

	releaseCmd.AddCommand(releaseShowCmd)
}
