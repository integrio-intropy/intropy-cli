package main

import (
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type promoteFlags struct {
	from    string
	to      string
	plan    bool
	noWait  bool
	timeout time.Duration
	deployTargetFlags
}

var promoteFlagValues = promoteFlags{deployTargetFlags: deployTargetFlags{output: deploy.OutputPlain}}

var deployPromoteCmd = &cobra.Command{
	Use:   "promote <component>",
	Short: "Copy the digests one environment runs into another",
	Long: "Copy the image digests a component has pinned in one environment into another environment's " +
		"overlay.\n\n" +
		"Promotion resolves nothing. It does not look a version up in the registry and does not read a source " +
		"repository — it reads the digests --from currently pins and writes those exact values.\n\n" +
		"Two policies in deploy.yaml are enforced rather than reported. The target's promotesFrom must permit " +
		"the edge, so dev → prod is refused when prod promotes from staging. And where the target sets " +
		"requireSourceHealthy, the source's ArgoCD application must be Synced and Healthy at the revision its " +
		"current digests were pinned by — a healthy application at some later revision does not show that these " +
		"bits ran.\n\n" +
		"With --plan the overlay is rendered and diffed but nothing is written to git. Environments that sync " +
		"manually record the intent and stop, printing the sync command to run.",
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(promoteFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if promoteFlagValues.from == "" {
			return newUsageErrorf("--from is required: a promotion copies digests out of a named environment")
		}
		if promoteFlagValues.to == "" {
			return newUsageErrorf("--to is required")
		}
		// Checked here as well as in the package, so a caller mistake exits 2
		// rather than 1 — the same treatment deploy gives its impossible flag
		// combinations.
		if promoteFlagValues.from == promoteFlagValues.to {
			return newUsageErrorf("--from and --to are both %s; a promotion moves digests between environments", promoteFlagValues.to)
		}

		ctx, cancel := commandContext(cmd)
		defer cancel()

		return deploy.Promote(ctx, deploy.PromoteOptions{
			Component:    args[0],
			From:         promoteFlagValues.from,
			To:           promoteFlagValues.to,
			Domain:       promoteFlagValues.domain,
			System:       promoteFlagValues.system,
			GitopsRepo:   promoteFlagValues.gitopsRepo,
			ArgocdServer: promoteFlagValues.argocdServer,
			PlanOnly:     promoteFlagValues.plan,
			NoWait:       promoteFlagValues.noWait,
			Timeout:      promoteFlagValues.timeout,
			OutputFormat: promoteFlagValues.output,
			Color:        useColor(cmd),
			UserAgent:    userAgent(),
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deployPromoteCmd.Flags()
	f.StringVar(&promoteFlagValues.from, "from", "", "environment to copy the pinned digests from (required)")
	f.StringVar(&promoteFlagValues.to, "to", "", "environment to write them into (required)")
	f.BoolVar(&promoteFlagValues.plan, "plan", false, flagUsagePlan)
	f.BoolVar(&promoteFlagValues.noWait, "no-wait", false, flagUsageNoWaitPush)
	f.DurationVar(&promoteFlagValues.timeout, "timeout", argocd.DefaultTimeout, flagUsageTimeout)
	promoteFlagValues.register(deployPromoteCmd)

	_ = deployPromoteCmd.MarkFlagRequired("from")
	_ = deployPromoteCmd.MarkFlagRequired("to")

	deployCmd.AddCommand(deployPromoteCmd)
}
