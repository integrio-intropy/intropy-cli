package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type deployFlags struct {
	env        string
	domain     string
	system     string
	gitopsRepo string
	plan       bool
	allowDirty bool
	output     string
}

var deployFlagValues = deployFlags{output: deploy.OutputPlain}

var deployCmd = &cobra.Command{
	Use:   "deploy <component>",
	Short: "Pin a component's image digest into an environment",
	Long: "Resolve the image digest that CI published for the current commit and pin it into one environment's " +
		"kustomize overlay in the GitOps repository.\n\n" +
		"Run it inside the component's source repository: the commit comes from HEAD there, and must be pushed — " +
		"CI builds pushed commits, so an unpushed one has no image. The component is located by searching the " +
		"GitOps repository for domains/*/*/<component>; pass --domain and --system if the name is ambiguous.\n\n" +
		"With --plan the overlay is rendered and diffed but nothing is written to git.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDeployComponents,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(deployFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if deployFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Run(ctx, deploy.Options{
			Component:    args[0],
			Domain:       deployFlagValues.domain,
			System:       deployFlagValues.system,
			Environment:  deployFlagValues.env,
			GitopsRepo:   deployFlagValues.gitopsRepo,
			PlanOnly:     deployFlagValues.plan,
			AllowDirty:   deployFlagValues.allowDirty,
			OutputFormat: deployFlagValues.output,
			Color:        useColor(cmd),
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
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

// completeDeployComponents suggests component names from the cached GitOps
// checkout. Like the other completions in this package it stays silent on
// error — a shell completion must never print a diagnostic.
func completeDeployComponents(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	root, err := gitops.CachedRoot(deployFlagValues.gitopsRepo)
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
	root, err := gitops.CachedRoot(deployFlagValues.gitopsRepo)
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
	f.BoolVar(&deployFlagValues.plan, "plan", false, "render and diff the change without writing to git")
	f.BoolVar(&deployFlagValues.allowDirty, "allow-dirty", false, "deploy despite uncommitted changes under the component's source paths")
	f.StringVarP(&deployFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	_ = deployCmd.MarkFlagRequired("env")
	_ = deployCmd.RegisterFlagCompletionFunc("env", completeDeployEnvironments)
	_ = deployCmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{deploy.OutputPlain, deploy.OutputJSON}, cobra.ShellCompDirectiveNoFileComp
	})

	rootCmd.AddCommand(deployCmd)
}
