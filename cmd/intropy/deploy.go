package main

import (
	"os"

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

func init() {
	rootCmd.AddCommand(deployCmd)
}
