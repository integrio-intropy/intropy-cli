package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type manifestsInspectFlags struct {
	system          string
	templateVersion string
	templateRepo    string
	output          string
}

var manifestsInspectFlagValues = manifestsInspectFlags{output: deploy.OutputPlain}

var manifestsInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the deployment model derived from a system topology",
	Long: "Read the system topology and scaffold records, resolve the template release, and print the components, " +
		"workloads, ports, and available local fixtures. Nothing is rendered or written, and Git is not touched.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateOutputFlag(manifestsInspectFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		owner, repo, err := resolveTemplateRepo(manifestsInspectFlagValues.templateRepo)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return deploy.InspectManifests(ctx, deploy.InspectManifestOptions{
			System:          manifestsInspectFlagValues.system,
			TemplateVersion: manifestsInspectFlagValues.templateVersion,
			OutputFormat:    manifestsInspectFlagValues.output,
			UserAgent:       "intropy-cli/" + version,
			Stdin:           cmd.InOrStdin(),
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
			Owner:           owner,
			Repo:            repo,
		})
	},
}

func init() {
	f := manifestsInspectCmd.Flags()
	f.StringVar(&manifestsInspectFlagValues.system, "system", "", flagUsageManifestSystem)
	f.StringVar(&manifestsInspectFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&manifestsInspectFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringVarP(&manifestsInspectFlagValues.output, "output", "o", deploy.OutputPlain, flagUsageOutput)
	manifestsCmd.AddCommand(manifestsInspectCmd)
}
