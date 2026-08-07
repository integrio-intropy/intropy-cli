package main

// Shared flag descriptions. A flag that appears on more than one command
// uses the constant here rather than a hand-written string, so the help
// text cannot drift between commands. See AGENTS.md.
const (
	// flagUsageNoInput is deliberately one constant for every command: a flag
	// that means "fail rather than ask" must read identically everywhere.

	// flagUsageOutput is the --output description for commands whose plain
	// output is human-readable and whose json output goes to stdout.
	flagUsageOutput = "output format (plain, json)"

	// flagUsageOutputJSONOnly is the --output description for the two
	// scaffold commands (int create, sys create), whose only format is a
	// JSON result document.
	flagUsageOutputJSONOnly = "output format: 'json' writes the result document to stdout"

	// flagUsageEnv is the --env description for deploy subcommands that
	// take a single target environment.
	flagUsageEnv = "target environment (required)"

	flagUsageDomain = "disambiguate the component by domain"
	flagUsageSystem = "disambiguate the component by system"

	// flagUsageInitDomain and flagUsageInitSystem are deliberately not the
	// shared constants: on deploy init, --domain is a destination in the
	// tree and --system selects a host to build, not filters on a search.
	flagUsageInitDomain   = "domain to place the system under (default: where it already is in the GitOps tree, else the workspace's domains/<domain>/ layout)"
	flagUsageInitSystem   = "system to scaffold; selects the host when the workspace holds several (default: the only one)"
	flagUsageGitopsRepo   = "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)"
	flagUsageArgocd       = "ArgoCD server address (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)"
	flagUsageTemplateVer  = "template release tag (default: latest)"
	flagUsageNoInput      = "never prompt; fail if a required value is missing"
	flagUsageTemplateRepo = "template library as owner/repo (default: templateRepo from config, or INTROPY_TEMPLATE_REPO)"
)
