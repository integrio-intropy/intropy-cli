package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type statusFlags struct {
	domain       string
	system       string
	gitopsRepo   string
	argocdServer string
	output       string
}

var statusFlagValues = statusFlags{output: deploy.OutputPlain}

var deployStatusCmd = &cobra.Command{
	Use:   "status <component>",
	Short: "Show what every environment runs, side by side",
	Long: "Report the release, image digest, age, sync state and health of a component in every environment " +
		"it is onboarded to, one row each.\n\n" +
		"This is the confirmation at the end of the release process. Promotion copies digests rather than " +
		"rebuilding, so the same digest in every row is what makes \"production runs the bits staging tested\" a " +
		"fact rather than a hope. The line under the table says whether that holds, and names the environments " +
		"that disagree when it does not.\n\n" +
		"The release, digest and age columns are read from the environment's overlay in the GitOps repository, " +
		"and dated by the commit that last changed it. Sync and health come from ArgoCD. An environment whose " +
		"overlay cannot be read, or that pins a tag rather than a digest, is reported rather than treated as an " +
		"error — the point of the table is the environments that are fine next to the one that is not. If ArgoCD " +
		"cannot be reached those two columns are left empty and everything else still prints.\n\n" +
		"Rows are ordered by the promotion graph in deploy.yaml, so the last row is the furthest downstream.\n\n" +
		"Nothing is written to git, no sync is triggered, and kubectl is never invoked. The exit code is 0 even " +
		"when the environments disagree; read `consistent` from --output json to gate on it.",
	Args:              cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(statusFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Status(ctx, deploy.StatusOptions{
			Component:    args[0],
			Domain:       statusFlagValues.domain,
			System:       statusFlagValues.system,
			GitopsRepo:   statusFlagValues.gitopsRepo,
			ArgocdServer: statusFlagValues.argocdServer,
			OutputFormat: statusFlagValues.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deployStatusCmd.Flags()
	f.StringVar(&statusFlagValues.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&statusFlagValues.system, "system", "", "disambiguate the component by system")
	f.StringVar(&statusFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVar(&statusFlagValues.argocdServer, "argocd-server", "", "ArgoCD server to read from (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)")
	f.StringVarP(&statusFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	// Deliberately no --env: status is every environment at once, and the
	// question about one of them — what a sync would apply — is deploy diff.

	deployCmd.AddCommand(deployStatusCmd)
}
