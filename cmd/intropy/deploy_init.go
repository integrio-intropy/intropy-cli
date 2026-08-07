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
	templateRepo    string
	values          []string
	sets            []string
	noInput         bool
	plan            bool
	force           bool
	gitopsRepo      string
	output          string
	local           bool
	namespace       string
	images          []string
}

var initFlagValues = initFlags{output: deploy.OutputPlain}

var deployInitCmd = &cobra.Command{
	Use:   "init [component...]",
	Short: "Scaffold a system's manifests into the GitOps repository",
	Long: "Generate the kustomize tree a system needs in the GitOps repository, from the topology its host " +
		"declares.\n\n" +
		"Each connector needs a Dapr binding per environment; the command asks once per connector and records " +
		"the answers in .intropy/deploy-values.yaml, checked in so the team scaffolds the same thing. Facts the " +
		"CLI still cannot derive — addresses, credentials, cron schedules — are emitted as REPLACE-ME-<HINT> " +
		"placeholders and listed when the run finishes. Image tags are not in that list: `intropy deploy` pins " +
		"digests, and scaffolding never pins one.\n\n" +
		"--domain places the system in the tree. Note this differs from every other deploy subcommand, where " +
		"--domain narrows a search: here it is a destination. It is rarely needed — the domain comes from where " +
		"the system already sits, or from the workspace's domains/<domain>/<system>/ layout. A workspace with " +
		"several systems needs --system to pick one.\n\n" +
		"The run pushes deploy-init/<domain>-<system> for review, never the default branch: a tree of " +
		"placeholders would be picked up by the ApplicationSet immediately. Re-running is additive; a file that " +
		"already differs is reported and left alone unless --force is given, and --force still refuses an " +
		"overlay that pins a digest. --plan writes nothing and does not touch git.\n\n" +
		"--local renders for the local development cluster to stdout instead, for piping to kubectl. See " +
		"docs/deploy-init.md for both destinations.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(initFlagValues.output, deploy.OutputPlain, deploy.OutputJSON); err != nil {
			return err
		}
		sets, err := template.ParseSets(initFlagValues.sets)
		if err != nil {
			return newUsageErrorf("%v", err)
		}

		owner, repo, err := resolveTemplateRepo(initFlagValues.templateRepo)
		if err != nil {
			return err
		}

		mode := deploy.ModeGitOps
		if initFlagValues.local {
			mode = deploy.ModeLocal
			for _, f := range []string{"domain", "environments", "values", "set", "plan", "force", "gitops-repo"} {
				if cmd.Flags().Changed(f) {
					return newUsageErrorf("--%s has no meaning with --local: a local render has no GitOps repository to place or push", f)
				}
			}
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return deploy.Init(ctx, deploy.InitOptions{
			Mode:            mode,
			Namespace:       initFlagValues.namespace,
			Images:          initFlagValues.images,
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
			Owner:           owner,
			Repo:            repo,
		})
	},
}

func init() {
	f := deployInitCmd.Flags()
	f.StringVar(&initFlagValues.domain, "domain", "", flagUsageInitDomain)
	f.StringVar(&initFlagValues.system, "system", "", flagUsageInitSystem)
	f.StringArrayVar(&initFlagValues.envs, "environments", nil, "environments to create overlays for (repeatable; default: every environment in deploy.yaml)")
	f.StringVar(&initFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&initFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringArrayVarP(&initFlagValues.values, "values", "f", nil, "values file (repeatable; - reads one document from stdin)")
	f.StringArrayVarP(&initFlagValues.sets, "set", "s", nil, "set a template value as key=value (repeatable)")
	f.BoolVar(&initFlagValues.noInput, "no-input", false, flagUsageNoInput)
	f.BoolVar(&initFlagValues.plan, "plan", false, "report what would be written without writing it or touching git")
	f.BoolVar(&initFlagValues.force, "force", false, "overwrite files that already differ (refused for an overlay that pins a digest)")
	f.StringVar(&initFlagValues.gitopsRepo, "gitops-repo", "", flagUsageGitopsRepo)
	f.StringVarP(&initFlagValues.output, "output", "o", deploy.OutputPlain, flagUsageOutput)
	f.BoolVar(&initFlagValues.local, "local", false, "render for the local development cluster to stdout, for piping to kubectl")
	f.StringVar(&initFlagValues.namespace, "namespace", "", "target namespace in the emitted manifests, with --local (default: the system name)")
	f.StringArrayVar(&initFlagValues.images, "image", nil, "image override, with --local: <component>=<name:tag> for one component, :<tag> for all (repeatable)")

	deployCmd.AddCommand(deployInitCmd)
}
