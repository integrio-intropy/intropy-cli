package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/spf13/cobra"
)

// release create publishes an immutable release manifest for HEAD. Digest
// resolution, notes generation, the manifest push, and the git tag all live
// in internal/release.Create; this file is flag plumbing.
type releaseCreateFlags struct {
	version    string
	domain     string
	system     string
	gitopsRepo string
	allowDirty bool
	watch      bool
	output     string
}

var releaseCreateOpts = releaseCreateFlags{output: release.OutputPlain}

var releaseCreateCmd = &cobra.Command{
	Use:   "create <component>",
	Short: "Publish an immutable release manifest",
	Long: "Resolve the image digests CI published for HEAD and record them, with the commit and generated notes, " +
		"as an immutable release manifest in the registry. An annotated git tag is pushed alongside it.\n\n" +
		"This changes no environment. Whatever each environment was running, it still is — and because the version " +
		"resolves the same commit, it resolves the same digests.\n\n" +
		"Run it inside the component's source repository. The commit comes from HEAD and must be pushed. Notes are " +
		"generated from the commits since the previous release that this one descends from.\n\n" +
		"If CI has not finished publishing the commit's images, the command fails — unless --watch is given, in " +
		"which case it polls the registry until the images appear, and proceeds from there.\n\n" +
		"Re-running for a version that already exists is safe: an identical release is recognised and only a missing " +
		"git tag is repaired. A different one is refused.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(releaseCreateOpts.output, release.OutputPlain, release.OutputJSON); err != nil {
			return err
		}
		if releaseCreateOpts.version == "" {
			return newUsageErrorf("--version is required")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return release.Create(ctx, release.Options{
			Component:    args[0],
			Domain:       releaseCreateOpts.domain,
			System:       releaseCreateOpts.system,
			Version:      releaseCreateOpts.version,
			GitopsRepo:   releaseCreateOpts.gitopsRepo,
			AllowDirty:   releaseCreateOpts.allowDirty,
			Watch:        releaseCreateOpts.watch,
			OutputFormat: releaseCreateOpts.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := releaseCreateCmd.Flags()
	f.StringVar(&releaseCreateOpts.version, "version", "", "version to publish (required)")
	f.StringVar(&releaseCreateOpts.domain, "domain", "", flagUsageDomain)
	f.StringVar(&releaseCreateOpts.system, "system", "", flagUsageSystem)
	f.StringVar(&releaseCreateOpts.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.BoolVar(&releaseCreateOpts.allowDirty, "allow-dirty", false, "release despite uncommitted changes under the component's source paths")
	f.BoolVarP(&releaseCreateOpts.watch, "watch", "w", false, "wait for the commit's images to appear in the registry instead of failing immediately")
	f.StringVarP(&releaseCreateOpts.output, "output", "o", release.OutputPlain, flagUsageOutput)

	_ = releaseCreateCmd.MarkFlagRequired("version")

	releaseCmd.AddCommand(releaseCreateCmd)
}
