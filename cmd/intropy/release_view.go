package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

type releaseViewFlags struct {
	domain     string
	system     string
	gitopsRepo string
	output     string
}

var releaseViewOpts = releaseViewFlags{output: release.OutputPlain}

var releaseViewCmd = &cobra.Command{
	Use:   "view <component> <version>",
	Short: "Read a published release manifest",
	Long: "Read the release manifest for a version and print what it records: the source commit, the pinned image " +
		"digests, what the notes were measured against, and the notes themselves.\n\n" +
		"It changes no source repository, GitOps remote, or environment. Use it to sanity-check generated notes before deploying anything from the release.",
	Args:              cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(releaseViewOpts.output, release.OutputPlain, release.OutputJSON); err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return release.View(ctx, release.Options{
			Component:    args[0],
			Version:      args[1],
			Domain:       releaseViewOpts.domain,
			System:       releaseViewOpts.system,
			GitopsRepo:   releaseViewOpts.gitopsRepo,
			OutputFormat: releaseViewOpts.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := releaseViewCmd.Flags()
	f.StringVar(&releaseViewOpts.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&releaseViewOpts.system, "system", "", "disambiguate the component by system")
	f.StringVar(&releaseViewOpts.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVarP(&releaseViewOpts.output, "output", "o", release.OutputPlain, "output format (plain, json)")

	releaseCmd.AddCommand(releaseViewCmd)
}
