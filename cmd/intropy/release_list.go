package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

// defaultReleaseListLimit keeps the common case short. A long-lived component
// accumulates more releases than anyone reads at once, and --limit 0 is there
// for when the whole history is what you want.
const defaultReleaseListLimit = 20

type releaseListFlags struct {
	domain     string
	system     string
	gitopsRepo string
	output     string
	limit      int
}

var releaseListOpts = releaseListFlags{output: release.OutputPlain, limit: defaultReleaseListLimit}

var releaseListCmd = &cobra.Command{
	Use:   "list <component>",
	Short: "List the releases published for a component",
	Long: "List the versions released for a component, newest first, with the date each was cut, its source commit, and the " +
		"first line of its notes. Pass one of those versions to release view to read the full manifest.\n\n" +
		"Releases are read from the registry beside the component's images, so this reports what is actually published rather " +
		"than what git tags claim. It changes no source repository, GitOps remote, or environment.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeReleaseComponents,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(releaseListOpts.output, release.OutputPlain, release.OutputJSON); err != nil {
			return err
		}
		if releaseListOpts.limit < 0 {
			return newUsageErrorf("--limit must not be negative (use 0 for all releases)")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return release.List(ctx, release.Options{
			Component:    args[0],
			Domain:       releaseListOpts.domain,
			System:       releaseListOpts.system,
			GitopsRepo:   releaseListOpts.gitopsRepo,
			OutputFormat: releaseListOpts.output,
			Limit:        releaseListOpts.limit,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := releaseListCmd.Flags()
	f.StringVar(&releaseListOpts.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&releaseListOpts.system, "system", "", "disambiguate the component by system")
	f.StringVar(&releaseListOpts.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVarP(&releaseListOpts.output, "output", "o", release.OutputPlain, "output format (plain, json)")
	f.IntVarP(&releaseListOpts.limit, "limit", "n", defaultReleaseListLimit, "maximum releases to show, newest first (0 for all)")

	_ = releaseListCmd.RegisterFlagCompletionFunc("output", completeReleaseOutput)

	releaseCmd.AddCommand(releaseListCmd)
}
