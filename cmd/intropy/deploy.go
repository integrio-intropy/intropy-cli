package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type deployFlags struct {
	env          string
	domain       string
	system       string
	gitopsRepo   string
	argocdServer string
	plan         bool
	allowDirty   bool
	watch        bool
	noWait       bool
	timeout      time.Duration
	output       string
}

var deployFlagValues = deployFlags{output: deploy.OutputPlain}

var deployCmd = &cobra.Command{
	Use:   "deploy <component> [version]",
	Short: "Pin a component's image digest into an environment",
	Long: "Pin the image digests for a component into one environment's kustomize overlay in the GitOps " +
		"repository.\n\n" +
		"Without a version the commit comes from HEAD in the current source repository, which must be clean " +
		"and pushed — CI builds pushed commits, so an unpushed one has no image — and the digests come from the " +
		"tags CI published for it. If CI has not finished yet, the command fails — unless --watch is given, in " +
		"which case it polls the registry until the images appear, and proceeds from there.\n\n" +
		"With a version the digests come from that release's manifest instead. The release already recorded " +
		"them, so no source repository is read and the command works from any directory. When the target " +
		"environment declares promotesFrom, the plan also says whether those digests are what the upstream " +
		"environment is already running.\n\n" +
		"The component is located by searching the GitOps repository for domains/*/*/<component>; pass --domain " +
		"and --system if the name is ambiguous.\n\n" +
		"With --plan the overlay is rendered and diffed but nothing is written to git. After pushing, the command " +
		"waits for ArgoCD to apply the new revision; --no-wait skips that, and environments that sync manually " +
		"never wait.\n\n" +
		"The subcommand names win over the component argument, so a component actually called diff, init, promote, " +
		"status or sync is not reachable this way.",
	Args:              cobra.RangeArgs(1, 2),
	ValidArgsFunction: completeDeployComponents,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(deployFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if deployFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}
		// Not named version: that is the package-level CLI version, and
		// shadowing it here would put the release into the user agent.
		releaseVersion := versionArg(args)
		// Ignoring a flag that cannot apply would leave the impression that a
		// working-tree check was waived when none was ever run.
		if releaseVersion != "" && deployFlagValues.allowDirty {
			return newUsageErrorf("--allow-dirty has no meaning when deploying a release: a release records the digests, so no working tree is read")
		}
		if releaseVersion != "" && deployFlagValues.watch {
			return newUsageErrorf("--watch has no meaning when deploying a release: a release records the digests, so there is nothing to wait for")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Run(ctx, deploy.Options{
			Component:    args[0],
			Version:      releaseVersion,
			Domain:       deployFlagValues.domain,
			System:       deployFlagValues.system,
			Environment:  deployFlagValues.env,
			GitopsRepo:   deployFlagValues.gitopsRepo,
			ArgocdServer: deployFlagValues.argocdServer,
			PlanOnly:     deployFlagValues.plan,
			AllowDirty:   deployFlagValues.allowDirty,
			Watch:        deployFlagValues.watch,
			NoWait:       deployFlagValues.noWait,
			Timeout:      deployFlagValues.timeout,
			OutputFormat: deployFlagValues.output,
			Color:        useColor(cmd),
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

// versionArg returns the optional release version.
func versionArg(args []string) string {
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

// useColor decides whether to emit ANSI colour: only for a real terminal, and
// only if neither --no-color nor the de-facto NO_COLOR convention objects.
func useColor(cmd *cobra.Command) bool {
	if noColorFlag || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := cmd.OutOrStdout().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// gitopsRepoFlag reads the invoked command's own --gitops-repo, so a completion
// works the same under deploy, deploy promote and deploy sync.
func gitopsRepoFlag(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("gitops-repo"); f != nil {
		return f.Value.String()
	}
	return ""
}

// completeDeployComponents suggests component names from the cached GitOps
// checkout. Like the other completions in this package it stays silent on
// error — a shell completion must never print a diagnostic.
func completeDeployComponents(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// The second argument is a release version, and listing those means a
	// registry round trip. Every completion here reads the local cache only.
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	root, err := gitops.CachedRoot(gitopsRepoFlag(cmd))
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

// completeDeployEnvironments suggests environments from the GitOps
// repository's deploy.yaml.
func completeDeployEnvironments(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	root, err := gitops.CachedRoot(gitopsRepoFlag(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := gitops.LoadDeployConfig(root)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cfg.EnvironmentNames(), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	f := deployCmd.Flags()
	f.StringVarP(&deployFlagValues.env, "env", "e", "", "target environment (required)")
	f.StringVar(&deployFlagValues.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&deployFlagValues.system, "system", "", "disambiguate the component by system")
	f.StringVar(&deployFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVar(&deployFlagValues.argocdServer, "argocd-server", "", "ArgoCD server to watch (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)")
	f.BoolVar(&deployFlagValues.plan, "plan", false, "render and diff the change without writing to git")
	f.BoolVar(&deployFlagValues.allowDirty, "allow-dirty", false, "deploy despite uncommitted changes under the component's source paths")
	f.BoolVarP(&deployFlagValues.watch, "watch", "w", false, "wait for the commit's images to appear in the registry instead of failing immediately")
	f.BoolVar(&deployFlagValues.noWait, "no-wait", false, "push without waiting for ArgoCD to sync")
	f.DurationVar(&deployFlagValues.timeout, "timeout", argocd.DefaultTimeout, "how long to wait for ArgoCD to converge")
	f.StringVarP(&deployFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	_ = deployCmd.MarkFlagRequired("env")
	_ = deployCmd.RegisterFlagCompletionFunc("env", completeDeployEnvironments)
	_ = deployCmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{deploy.OutputPlain, deploy.OutputJSON}, cobra.ShellCompDirectiveNoFileComp
	})

	rootCmd.AddCommand(deployCmd)
}
