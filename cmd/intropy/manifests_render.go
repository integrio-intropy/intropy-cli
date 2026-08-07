package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/spf13/cobra"
)

type manifestsRenderFlags struct {
	env             string
	system          string
	templateVersion string
	templateRepo    string
	namespace       string
	images          []string
	bindings        []string
}

var manifestsRenderFlagValues manifestsRenderFlags

var manifestsRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a system's Kubernetes manifests as YAML",
	Long: "Render and validate the complete manifest stream for one environment. For local development, YAML is " +
		"written to stdout for piping to kubectl; progress, prompts, and errors are written to stderr. Use --binding " +
		"for reproducible connector fixtures. Missing choices are prompted for when the terminal is interactive and fail " +
		"clearly otherwise. The complete render is buffered, so a failed render writes no YAML. Nothing is written to Git.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if manifestsRenderFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}
		if manifestsRenderFlagValues.env != "local" {
			return newUsageErrorf("--env must be local; use 'intropy manifests create --env <environment>' for GitOps manifests")
		}
		owner, repo, err := resolveTemplateRepo(manifestsRenderFlagValues.templateRepo)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		selector := interactive.NewTerminalSelector(cmd.InOrStdin(), cmd.ErrOrStderr())
		built, err := deploy.RenderManifests(ctx, deploy.RenderManifestOptions{
			Environment:     manifestsRenderFlagValues.env,
			System:          manifestsRenderFlagValues.system,
			TemplateVersion: manifestsRenderFlagValues.templateVersion,
			Namespace:       manifestsRenderFlagValues.namespace,
			Images:          manifestsRenderFlagValues.images,
			Bindings:        manifestsRenderFlagValues.bindings,
			Selector:        selector,
			UserAgent:       "intropy-cli/" + version,
			Stdin:           cmd.InOrStdin(),
			Stderr:          cmd.ErrOrStderr(),
			Owner:           owner,
			Repo:            repo,
		})
		if err != nil {
			return err
		}
		n, err := cmd.OutOrStdout().Write(built)
		if err != nil {
			return fmt.Errorf("write manifests: %w", err)
		}
		if n != len(built) {
			return fmt.Errorf("write manifests: wrote %d of %d bytes", n, len(built))
		}
		return nil
	},
}

func init() {
	f := manifestsRenderCmd.Flags()
	f.StringVarP(&manifestsRenderFlagValues.env, "env", "e", "", flagUsageEnv)
	f.StringVar(&manifestsRenderFlagValues.system, "system", "", flagUsageManifestSystem)
	f.StringVar(&manifestsRenderFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&manifestsRenderFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringVar(&manifestsRenderFlagValues.namespace, "namespace", "", "target namespace (default: the system name)")
	f.StringArrayVar(&manifestsRenderFlagValues.images, "image", nil, "image override: <component>=<name:tag> for one component, :<tag> for all (repeatable)")
	f.StringArrayVar(&manifestsRenderFlagValues.bindings, "binding", nil, "local connector fixture as <connector>=<fixture> (repeatable)")
	_ = manifestsRenderCmd.MarkFlagRequired("env")
	manifestsCmd.AddCommand(manifestsRenderCmd)
}
