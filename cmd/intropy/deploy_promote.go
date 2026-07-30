package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type promoteFlags struct {
	from         string
	to           string
	domain       string
	system       string
	gitopsRepo   string
	argocdServer string
	plan         bool
	noWait       bool
	timeout      time.Duration
	output       string
}

var promoteFlagValues = promoteFlags{output: deploy.OutputPlain}

var deployPromoteCmd = &cobra.Command{
	Use:   "promote <component>",
	Short: "Copy the digests one environment runs into another",
	Long: "Copy the image digests a component has pinned in one environment into another environment's " +
		"overlay.\n\n" +
		"Promotion resolves nothing. It does not look a version up in the registry and does not read a source " +
		"repository — it reads the digests --from currently pins and writes those exact values. A release tag " +
		"that has since been moved, or a registry that answers differently than it did an hour ago, therefore " +
		"cannot change what the target ends up running.\n\n" +
		"Two policies in deploy.yaml are enforced rather than reported. The target's promotesFrom must permit " +
		"the edge, so dev → prod is refused when prod promotes from staging. And where the target sets " +
		"requireSourceHealthy, the source's ArgoCD application must be Synced and Healthy at the revision its " +
		"current digests were pinned by — a healthy application at some later revision does not show that these " +
		"bits ran.\n\n" +
		"With --plan the overlay is rendered and diffed but nothing is written to git. Environments that sync " +
		"manually record the intent and stop, printing the sync command to run.",
	Args:              cobra.ExactArgs(1),
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

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
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
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deployPromoteCmd.Flags()
	f.StringVar(&promoteFlagValues.from, "from", "", "environment to copy the pinned digests from (required)")
	f.StringVar(&promoteFlagValues.to, "to", "", "environment to write them into (required)")
	f.StringVar(&promoteFlagValues.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&promoteFlagValues.system, "system", "", "disambiguate the component by system")
	f.StringVar(&promoteFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVar(&promoteFlagValues.argocdServer, "argocd-server", "", "ArgoCD server to consult (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)")
	f.BoolVar(&promoteFlagValues.plan, "plan", false, "render and diff the change without writing to git")
	f.BoolVar(&promoteFlagValues.noWait, "no-wait", false, "push without waiting for ArgoCD to sync")
	f.DurationVar(&promoteFlagValues.timeout, "timeout", argocd.DefaultTimeout, "how long to wait for ArgoCD to converge")
	f.StringVarP(&promoteFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	_ = deployPromoteCmd.MarkFlagRequired("from")
	_ = deployPromoteCmd.MarkFlagRequired("to")

	deployCmd.AddCommand(deployPromoteCmd)
}
