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

	// Manifest generation places a whole system rather than disambiguating one
	// component, so these flags cannot use deploy's search descriptions.
	flagUsageManifestDomain = "domain to place the system under (default: where it already is in the GitOps tree, else the workspace layout)"
	flagUsageManifestSystem = "system to inspect or generate; selects the host when the workspace holds several"
	flagUsageGitopsRepo     = "GitOps repository URL (default: gitopsRepo from config, or INTROPY_GITOPS_REPO)"
	flagUsageArgocd         = "ArgoCD server address (default: argocdServer from config, ARGOCD_SERVER, or deploy.yaml)"
	flagUsageTemplateVer    = "template release tag (default: latest)"
	flagUsageNoInput        = "never prompt; fail if a required value is missing"
	flagUsageTemplateRepo   = "template library as owner/repo (default: templateRepo from config, or INTROPY_TEMPLATE_REPO)"
)
