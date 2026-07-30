package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

// deploy init scaffolds a system's kustomize tree into the GitOps
// repository. Everything non-trivial — topology discovery, rendering,
// staging, classification, push — lives in internal/deploy.Init; this file
// is flag plumbing and the call.
type initFlags struct {
	domain          string
	system          string
	envs            []string
	templateVersion string
	values          []string
	sets            []string
	noInput         bool
	plan            bool
	force           bool
	gitopsRepo      string
	output          string
}

var initFlagValues = initFlags{output: deploy.OutputPlain}

var deployInitCmd = &cobra.Command{
	Use:   "init [component...]",
	Short: "Scaffold a system's manifests into the GitOps repository",
	Long: "Generate the kustomize tree a system needs in the GitOps repository, from the topology its host " +
		"declares.\n\n" +
		"Facts the CLI cannot derive — connection strings, hosts, credentials, cron schedules — are emitted as " +
		"REPLACE-ME-<HINT> placeholders and listed when the run finishes. Image tags are not in that list: " +
		"`intropy deploy` pins digests, and scaffolding never pins one.\n\n" +
		"--domain places the system in the tree. Note this differs from every other deploy subcommand, where " +
		"--domain narrows a search: here it is a destination. It is rarely needed — the domain comes from where " +
		"the system already sits, or from the workspace's domains/<domain>/<system>/ layout. A workspace with " +
		"several systems needs --system to pick one.\n\n" +
		"The run pushes deploy-init/<domain>-<system> for review, never the default branch: a tree of " +
		"placeholders would be picked up by the ApplicationSet immediately. Re-running is additive; a file that " +
		"already differs is reported and left alone unless --force is given, and --force still refuses an " +
		"overlay that pins a digest. --plan writes nothing and does not touch git.\n\n" +
		"See docs/deploy-init.md for the layout rationale and the Dapr ownership model.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(initFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		sets, err := template.ParseSets(initFlagValues.sets)
		if err != nil {
			return newUsageErrorf("%v", err)
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Init(ctx, deploy.InitOptions{
			Components:      args,
			Domain:          initFlagValues.domain,
			System:          initFlagValues.system,
			Environments:    initFlagValues.envs,
			TemplateVersion: initFlagValues.templateVersion,
			Files:           initFlagValues.values,
			SetValues:       sets,
			NoInput:         initFlagValues.noInput,
			PlanOnly:        initFlagValues.plan,
			Force:           initFlagValues.force,
			GitopsRepo:      initFlagValues.gitopsRepo,
			OutputFormat:    initFlagValues.output,
			Color:           useColor(cmd),
			UserAgent:       "intropy-cli/" + version,
			CliVersion:      version,
			Stdin:           cmd.InOrStdin(),
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
		})
	},
}

func init() {
	f := deployInitCmd.Flags()
	f.StringVar(&initFlagValues.domain, "domain", "", flagUsageInitDomain)
	f.StringVar(&initFlagValues.system, "system", "", flagUsageInitSystem)
	f.StringArrayVar(&initFlagValues.envs, "environments", nil, "environments to create overlays for (repeatable; default: every environment in deploy.yaml)")
	f.StringVar(&initFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringArrayVarP(&initFlagValues.values, "values", "f", nil, "values file (repeatable; - reads one document from stdin)")
	f.StringArrayVarP(&initFlagValues.sets, "set", "s", nil, "set a template value as key=value (repeatable)")
	f.BoolVar(&initFlagValues.noInput, "no-input", false, "never prompt; fail if a required value is missing")
	f.BoolVar(&initFlagValues.plan, "plan", false, "report what would be written without writing it or touching git")
	f.BoolVar(&initFlagValues.force, "force", false, "overwrite files that already differ (refused for an overlay that pins a digest)")
	f.StringVar(&initFlagValues.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.StringVarP(&initFlagValues.output, "output", "o", deploy.OutputPlain, flagUsageOutput)

	deployCmd.AddCommand(deployInitCmd)
}
