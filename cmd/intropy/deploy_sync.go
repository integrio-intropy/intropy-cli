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

type syncFlags struct {
	env          string
	revision     string
	domain       string
	system       string
	gitopsRepo   string
	argocdServer string
	noWait       bool
	timeout      time.Duration
	output       string
}

var syncFlagValues = syncFlags{output: deploy.OutputPlain}

var deploySyncCmd = &cobra.Command{
	Use:   "sync <component>",
	Short: "Apply an environment's pending change through ArgoCD",
	Long: "Apply the GitOps change already committed for one environment, by asking ArgoCD to sync its " +
		"application.\n\n" +
		"This is the other half of an environment with sync: manual. A deploy or a promotion into such an " +
		"environment records intent by pushing a commit and stops; nothing reaches the cluster until someone " +
		"with the rights to apply it runs this. The authorisation and the audit trail therefore live in ArgoCD, " +
		"which evaluates the caller through its own RBAC, rather than in an approval on a YAML edit.\n\n" +
		"The revision synced is the commit that last changed the environment's overlay, not the branch head. " +
		"Pass --revision to name the commit whose diff you reviewed: if the pending change is a different one, " +
		"the sync is refused rather than spending your approval on something you did not read.\n\n" +
		"Nothing is written to git, and kubectl is never invoked.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(syncFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if syncFlagValues.env == "" {
			return newUsageErrorf("--env is required")
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Sync(ctx, deploy.SyncOptions{
			Component:    args[0],
			Environment:  syncFlagValues.env,
			Revision:     syncFlagValues.revision,
			Domain:       syncFlagValues.domain,
			System:       syncFlagValues.system,
			GitopsRepo:   syncFlagValues.gitopsRepo,
			ArgocdServer: syncFlagValues.argocdServer,
			NoWait:       syncFlagValues.noWait,
			Timeout:      syncFlagValues.timeout,
			OutputFormat: syncFlagValues.output,
			UserAgent:    "intropy-cli/" + version,
			Stdout:       cmd.OutOrStdout(),
			Stderr:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deploySyncCmd.Flags()
	f.StringVarP(&syncFlagValues.env, "env", "e", "", "environment to apply (required)")
	f.StringVar(&syncFlagValues.revision, "revision", "", "the commit whose diff you reviewed; refuse if the pending change is another one")
	f.StringVar(&syncFlagValues.domain, "domain", "", "disambiguate the component by domain")
	f.StringVar(&syncFlagValues.system, "system", "", "disambiguate the component by system")
	f.StringVar(&syncFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVar(&syncFlagValues.argocdServer, "argocd-server", "", "ArgoCD server to sync through (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)")
	f.BoolVar(&syncFlagValues.noWait, "no-wait", false, "return once the sync is accepted, without waiting for it to converge")
	f.DurationVar(&syncFlagValues.timeout, "timeout", argocd.DefaultTimeout, "how long to wait for ArgoCD to converge")
	f.StringVarP(&syncFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	_ = deploySyncCmd.MarkFlagRequired("env")

	deployCmd.AddCommand(deploySyncCmd)
}
