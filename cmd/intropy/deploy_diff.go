package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type diffFlags struct {
	env          string
	domain       string
	system       string
	gitopsRepo   string
	argocdServer string
	output       string
}

var diffFlagValues = diffFlags{output: deploy.OutputPlain}

var deployDiffCmd = &cobra.Command{
	Use:   "diff <component>",
	Short: "Show the rendered change a sync would apply",
	Long: "Render an environment's manifests at the revision ArgoCD has applied and at the revision " +
		"'deploy sync' would apply next, and print the difference.\n\n" +
		"This is the review half of an environment with sync: manual. It answers the only question an approver " +
		"has — what changes in the cluster if I sync this — with the resources themselves rather than a one-line " +
		"image pin.\n\n" +
		"Not deploy --plan, which diffs an uncommitted edit against the current worktree for the person writing " +
		"the change. Nothing here is uncommitted: both sides are commits, so everything between them counts, " +
		"including a base that moved and deployments that stacked up unapplied.\n\n" +
		"ArgoCD does the rendering, because the Application is the whole input: overrides it carries are " +
		"invisible to a local kustomize build, and a diff that is not what gets applied is worse than no diff. " +
		"ArgoCD must therefore be reachable.\n\n" +
		"A non-empty diff still exits 0 — this reports, it does not gate. Nothing is written to git, and " +
		"kubectl is never invoked.",
	Args:              cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(diffFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if diffFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Diff(ctx, deploy.DiffOptions{
			Component:    args[0],
			Environment:  diffFlagValues.env,
			Domain:       diffFlagValues.domain,
			System:       diffFlagValues.system,
			GitopsRepo:   diffFlagValues.gitopsRepo,
			ArgocdServer: diffFlagValues.argocdServer,
			OutputFormat: diffFlagValues.output,
			Color:        useColor(cmd),
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deployDiffCmd.Flags()
	f.StringVarP(&diffFlagValues.env, "env", "e", "", "environment to review (required)")
	f.StringVar(&diffFlagValues.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&diffFlagValues.system, "system", "", "disambiguate the component by system")
	f.StringVar(&diffFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVar(&diffFlagValues.argocdServer, "argocd-server", "", "ArgoCD server to render through (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)")
	f.StringVarP(&diffFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	_ = deployDiffCmd.MarkFlagRequired("env")

	deployCmd.AddCommand(deployDiffCmd)
}
