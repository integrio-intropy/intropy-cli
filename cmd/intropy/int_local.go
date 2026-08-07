package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

// int local renders a system's manifests for the local development cluster
// to stdout. Everything non-trivial — topology discovery, the connector
// prompt, rendering, the kustomize build — lives in internal/deploy.Local;
// this file is flag plumbing and the call.
type localFlags struct {
	namespace       string
	images          []string
	templateVersion string
	templateRepo    string
	noInput         bool
}

var intLocalFlags localFlags

var intLocalCmd = &cobra.Command{
	Use:   "local <system>",
	Short: "Render a system's manifests for the local development cluster",
	Long: "Render deployable manifests for the local development cluster to stdout, for piping to kubectl: " +
		"`intropy int local <system> | kubectl apply -f -`.\n\n" +
		"The topology comes from the system host, exactly as deploy init reads it. Each connector needs a " +
		"fixture binding; the command asks once per connector and records the answers in .intropy/local.yaml, " +
		"checked in so the team renders the same thing. The fixture catalog comes from the fetched template " +
		"library release.\n\n" +
		"Component images render as <component>:dev. --image overrides that: --image <component>=<name:tag> " +
		"for one component, --image :<tag> for all.\n\n" +
		"The command never inspects the cluster and writes nothing but the state file; a missing cluster " +
		"fails at apply time with kubectl's own error. See docs/deploy-local.md for the boundary between this " +
		"command, the k3s scripts and the template overlays.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		owner, repo, err := resolveTemplateRepo(intLocalFlags.templateRepo)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Local(ctx, deploy.LocalOptions{
			System:          args[0],
			Namespace:       intLocalFlags.namespace,
			Images:          intLocalFlags.images,
			NoInput:         intLocalFlags.noInput,
			TemplateVersion: intLocalFlags.templateVersion,
			UserAgent:       "intropy-cli/" + version,
			Owner:           owner,
			Repo:            repo,
			Stdin:           cmd.InOrStdin(),
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := intLocalCmd.Flags()
	f.StringVar(&intLocalFlags.namespace, "namespace", "", "target namespace in the emitted manifests (default: the system name)")
	f.StringArrayVar(&intLocalFlags.images, "image", nil, "image override: <component>=<name:tag> for one component, :<tag> for all (repeatable)")
	f.StringVar(&intLocalFlags.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&intLocalFlags.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.BoolVar(&intLocalFlags.noInput, "no-input", false, flagUsageNoInput)
	intCmd.AddCommand(intLocalCmd)
}
