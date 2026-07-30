package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type initFlags struct {
	domain          string
	system          string
	envs            []string
	topology        string
	sourceDir       string
	templateVersion string
	version         string // deprecated alias for templateVersion
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
		"The topology comes from the system host's graph verb, which builds the project first and can take a " +
		"minute. Pass --topology with a file (or - for stdin) to skip that — in CI, capture it once with " +
		"`dotnet run --project <host> -- graph > topology.json`.\n\n" +
		"A workspace holding several systems needs --system to say which one. It is matched against each " +
		"scaffolded host's recorded system name and its system directory, both of which are on disk, so " +
		"picking a system never builds the others.\n\n" +
		"The manifests come from the template library's latest release; --version renders from a specific tag " +
		"instead, which is what makes a re-run reproducible while the templates are still moving.\n\n" +
		"Nothing is pushed to the default branch: a tree full of placeholders would be picked up by the " +
		"ApplicationSet immediately. The run pushes deploy-init/<domain>-<system> for review instead. Re-running " +
		"is additive and safe — a file that already exists and differs is reported and left alone unless --force " +
		"is given, and --force still refuses to overwrite an overlay that pins a digest.\n\n" +
		"With --plan nothing is written and git is not touched at all.",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeInitComponents,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(initFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		if err := resolveTemplateVersion(cmd, &initFlagValues.templateVersion, &initFlagValues.version); err != nil {
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
			TopologyFile:    initFlagValues.topology,
			SourceDir:       initFlagValues.sourceDir,
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

// completeInitComponents suggests block names from the local workspace.
//
// Deliberately local: the alternative is the topology, and obtaining that means
// a dotnet build. A completion that takes a minute is not a completion.
func completeInitComponents(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	dir := cmd.Flags().Lookup("source-dir").Value.String()
	if dir == "" {
		dir = "."
	}
	entries, _ := template.ListScaffolds(dir)
	var names []string
	for _, e := range entries {
		if e.Role == template.RoleSystemHost {
			continue
		}
		names = append(names, filepath.Base(e.Path))
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeDeployDomains suggests the domains already in the GitOps tree, from
// the cached checkout only.
func completeDeployDomains(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	root, err := gitops.CachedRoot(gitopsRepoFlag(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var names []string
	for _, c := range gitops.ListComponents(root) {
		if !seen[c.Domain] {
			seen[c.Domain] = true
			names = append(names, c.Domain)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	f := deployInitCmd.Flags()
	f.StringVar(&initFlagValues.domain, "domain", "", "domain to place the system under (default: where it already is in the GitOps tree, else the workspace's domains/<domain>/ layout)")
	f.StringVar(&initFlagValues.system, "system", "", "system to scaffold; selects the host when the workspace holds several (default: the only one)")
	f.StringSliceVarP(&initFlagValues.envs, "env", "e", nil, "environments to create overlays for (default: every environment in deploy.yaml)")
	f.StringVar(&initFlagValues.topology, "topology", "", "read the topology record from a file instead of running the host's graph verb (- for stdin)")
	f.StringVar(&initFlagValues.sourceDir, "source-dir", ".", "workspace to discover the system host and scaffold records in")
	registerTemplateVersionFlag(deployInitCmd, &initFlagValues.templateVersion, &initFlagValues.version)
	f.StringSliceVarP(&initFlagValues.values, "values", "f", nil, "values file (repeatable; - reads one document from stdin)")
	f.StringArrayVarP(&initFlagValues.sets, "set", "s", nil, "set a template value as key=value (repeatable)")
	f.BoolVar(&initFlagValues.noInput, "no-input", false, "never prompt; fail if a required value is missing")
	f.BoolVar(&initFlagValues.plan, "plan", false, "report what would be written without writing it or touching git")
	f.BoolVar(&initFlagValues.force, "force", false, "overwrite files that already differ (refused for an overlay that pins a digest)")
	f.StringVar(&initFlagValues.gitopsRepo, "gitops-repo", "", "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)")
	f.StringVarP(&initFlagValues.output, "output", "o", deploy.OutputPlain, "output format (plain, json)")

	// Deliberately no --argocd-server, --no-wait or --timeout: this writes a
	// branch for review and syncs nothing, so there is no ArgoCD interaction to
	// configure. No --allow-dirty either — no source working tree is read for
	// correctness, only for the topology the host itself reports.
	_ = deployInitCmd.RegisterFlagCompletionFunc("domain", completeDeployDomains)
	_ = deployInitCmd.RegisterFlagCompletionFunc("env", completeDeployEnvironments)
	_ = deployInitCmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{deploy.OutputPlain, deploy.OutputJSON}, cobra.ShellCompDirectiveNoFileComp
	})

	deployCmd.AddCommand(deployInitCmd)
}
