package main

import (
	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type statusFlags struct {
	deployTargetFlags
}

var statusFlagValues = statusFlags{deployTargetFlags: deployTargetFlags{output: deploy.OutputPlain}}

var deployStatusCmd = &cobra.Command{
	Use:   "status <component>",
	Short: "Show what every environment runs, side by side",
	Long: "Report the release, image digest, age, sync state and health of a component in every environment " +
		"it is onboarded to, one row each, and a line under the table saying whether they agree.\n\n" +
		"Release, digest and age come from the environment's overlay in the GitOps repository, dated by the " +
		"commit that last changed it. Sync and health come from ArgoCD; if ArgoCD cannot be reached those two " +
		"columns are left empty. An environment whose overlay cannot be read, or that pins a tag rather than a " +
		"digest, is reported rather than treated as an error. Rows follow the promotion graph in deploy.yaml, " +
		"last row furthest downstream.\n\n" +
		"Nothing is written to git, no sync is triggered, and the exit code is 0 even when environments " +
		"disagree. Read `consistent` from --output json to gate on it.",
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(statusFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}

		ctx, cancel := commandContext(cmd)
		defer cancel()

		return deploy.Status(ctx, deploy.StatusOptions{
			Component:    args[0],
			Domain:       statusFlagValues.domain,
			System:       statusFlagValues.system,
			GitopsRepo:   statusFlagValues.gitopsRepo,
			ArgocdServer: statusFlagValues.argocdServer,
			OutputFormat: statusFlagValues.output,
			UserAgent:    userAgent(),
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	statusFlagValues.register(deployStatusCmd)

	// Deliberately no --env: status is every environment at once, and the
	// question about one of them — what a sync would apply — is deploy diff.

	deployCmd.AddCommand(deployStatusCmd)
}
