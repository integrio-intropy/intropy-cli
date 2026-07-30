package main

import (
	"os"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// deployCmd is a pure parent: the digest-pinning command lives at
// 'deploy pin', symmetric with 'release create'. Keeping deploy non-runnable
// means no component name is ever shadowed by a subcommand — a component
// called diff, init, pin, promote, status or sync deploys like any other.
var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Move components between environments via the GitOps repository",
	Long: "Pin image digests into environments, promote them along the environment graph, and review or " +
		"apply the pending result — all through the GitOps repository and ArgoCD, never kubectl.\n\n" +
		"  pin      Pin a component's image digest into an environment\n" +
		"  promote  Copy the digests one environment runs into another\n" +
		"  diff     Show the rendered change a sync would apply\n" +
		"  status   Show what every environment runs, side by side\n" +
		"  sync     Apply an environment's pending change through ArgoCD\n" +
		"  init     Scaffold a system's manifests into the GitOps repository",
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
	rootCmd.AddCommand(deployCmd)
}
