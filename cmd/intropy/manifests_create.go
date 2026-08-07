package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type manifestsCreateFlags struct {
	env             string
	domain          string
	system          string
	templateVersion string
	templateRepo    string
	gitopsRepo      string
	dryRun          bool
	diff            bool
}

var manifestsCreateFlagValues manifestsCreateFlags

var manifestsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create missing manifests on a GitOps review branch",
	Long: "Render one environment from the system topology and create its missing files on a " +
		"manifests-create/<domain>-<system>-<environment> review branch. Existing identical files are accepted, " +
		"but an existing file that differs is never replaced or deleted. The default branch is never updated directly. " +
		"Use --dry-run for the file plan or --diff for the generated file differences; neither creates manifest files, commits, or pushes.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if manifestsCreateFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}
		if manifestsCreateFlagValues.env == "local" {
			return newUsageErrorf("--env local belongs to 'intropy manifests render --env local'")
		}
		owner, repo, err := resolveTemplateRepo(manifestsCreateFlagValues.templateRepo)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return deploy.CreateManifests(ctx, deploy.CreateManifestOptions{
			Environment:     manifestsCreateFlagValues.env,
			Domain:          manifestsCreateFlagValues.domain,
			System:          manifestsCreateFlagValues.system,
			TemplateVersion: manifestsCreateFlagValues.templateVersion,
			GitopsRepo:      manifestsCreateFlagValues.gitopsRepo,
			DryRun:          manifestsCreateFlagValues.dryRun,
			Diff:            manifestsCreateFlagValues.diff,
			UserAgent:       "intropy-cli/" + version,
			CliVersion:      version,
			Stdin:           cmd.InOrStdin(),
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
			Owner:           owner,
			Repo:            repo,
		})
	},
}

func init() {
	f := manifestsCreateCmd.Flags()
	f.StringVarP(&manifestsCreateFlagValues.env, "env", "e", "", flagUsageEnv)
	f.StringVar(&manifestsCreateFlagValues.domain, "domain", "", flagUsageManifestDomain)
	f.StringVar(&manifestsCreateFlagValues.system, "system", "", flagUsageManifestSystem)
	f.StringVar(&manifestsCreateFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&manifestsCreateFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringVar(&manifestsCreateFlagValues.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.BoolVar(&manifestsCreateFlagValues.dryRun, "dry-run", false, "report file actions without creating manifest files, commits, or pushes")
	f.BoolVar(&manifestsCreateFlagValues.diff, "diff", false, "print generated file differences without creating manifest files, commits, or pushes")
	_ = manifestsCreateCmd.MarkFlagRequired("env")
	manifestsCmd.AddCommand(manifestsCreateCmd)
}
