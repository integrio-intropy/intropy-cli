package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

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
		"Onboarding a component is otherwise entirely manual: somebody hand-writes component.yaml, base/ and an " +
		"overlay per environment, for every block, in every customer repository. This does that, and fills in " +
		"every fact the CLI can derive — the block's workload, the app-ids that belong in a Dapr component's " +
		"scopes, the environments deploy.yaml defines, the registry.\n\n" +
		"Complete manifests are not the goal. Connection strings, hosts, credentials and cron schedules cannot be " +
		"derived, so they are emitted as REPLACE-ME-<HINT> placeholders and reported as a list when the run " +
		"finishes. Filling those in is the developer's remaining job. Image tags are not in that list: " +
		"`intropy deploy` pins digests, and scaffolding must never pin one.\n\n" +
		"The system's shared objects — the Dapr pub/sub and secret store its blocks resolve by name, and the " +
		"secrets behind them — go in a `host` directory of their own. A Dapr Component is namespace-scoped and " +
		"every integration in the namespace shares it, so exactly one ArgoCD Application may own each one; the " +
		"host directory is what gives them that owner, and scopes: is what limits who may use them.\n\n" +
		"--domain places the system in the tree. Note this differs from every other deploy subcommand, where " +
		"--domain narrows a search: here it is a destination. You rarely need it: the domain comes from where " +
		"the system already sits in the GitOps tree, and failing that from the workspace's own " +
		"domains/<domain>/<system>/ layout, which every integrations tree mirrors from the deployment tree. " +
		"Pass it explicitly to place a system somewhere else, or when the workspace has another shape.\n\n" +
		"A workspace holding several systems needs --system to say which one. It is matched against each " +
		"scaffolded host's recorded system name and its system directory, both of which are on disk, so " +
		"picking a system never builds the others.\n\n" +
		"The manifests come from the template library's latest release; --template-version renders from a specific tag " +
		"instead, which is what makes a re-run reproducible while the templates are still moving.\n\n" +
		"Nothing is pushed to the default branch: a tree full of placeholders would be picked up by the " +
		"ApplicationSet immediately. The run pushes deploy-init/<domain>-<system> for review instead. Re-running " +
		"is additive and safe — a file that already exists and differs is reported and left alone unless --force " +
		"is given, and --force still refuses to overwrite an overlay that pins a digest.\n\n" +
		"With --plan nothing is written and git is not touched at all.",
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
	f.StringVar(&initFlagValues.domain, "domain", "", "domain to place the system under (default: where it already is in the GitOps tree, else the workspace's domains/<domain>/ layout)")
	f.StringVar(&initFlagValues.system, "system", "", "system to scaffold; selects the host when the workspace holds several (default: the only one)")
	f.StringArrayVar(&initFlagValues.envs, "environments", nil, "environments to create overlays for (repeatable; default: every environment in deploy.yaml)")
	f.StringVar(&initFlagValues.templateVersion, "template-version", "", "template release tag (default: latest)")
	f.StringArrayVarP(&initFlagValues.values, "values", "f", nil, "values file (repeatable; - reads one document from stdin)")
	f.StringArrayVarP(&initFlagValues.sets, "set", "s", nil, "set a template value as key=value (repeatable)")
	f.BoolVar(&initFlagValues.noInput, "no-input", false, "never prompt; fail if a required value is missing")
	f.BoolVar(&initFlagValues.plan, "plan", false, "report what would be written without writing it or touching git")
	f.BoolVar(&initFlagValues.force, "force", false, "overwrite files that already differ (refused for an overlay that pins a digest)")
	f.StringVar(&initFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVarP(&initFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	deployCmd.AddCommand(deployInitCmd)
}
