package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/huandu/xstrings"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// The templates manifest generation renders. They are separate packages because they
// render a different number of times — once per system, once per component —
// into different directories, from different value sets.
const (
	TemplateDeployHost      = "deploy-host"
	TemplateDeployComponent = "deploy-component"
)

// HostDirName is the component directory holding a system's shared manifests.
//
// A Dapr Component is namespace-scoped and every integration in a customer's
// namespace shares it, so exactly one ArgoCD Application may own each one. The
// system host is what gives them that owner: it has no Dockerfile and therefore
// no image, which is precisely what gitops.KindShared describes.
const HostDirName = "host"

// Review branches keep incomplete manifests away from the default branch,
// where the ApplicationSet could pick them up before review.
const manifestsCreateBranchPrefix = "manifests-create/"

// runGraph is the seam tests replace to avoid a dotnet build.
var runGraph = topology.RunGraph

// manifestMode selects where a scaffold lands.
type manifestMode int

const (
	// modeGitOps scaffolds into the GitOps repository and pushes a review
	// branch. The default.
	modeGitOps manifestMode = iota

	// modeLocal renders for the local development cluster and returns the
	// complete built manifest stream. Nothing touches Git.
	modeLocal
)

// manifestRunOptions carries the shared inputs after a manifests command has
// applied its own flag and side-effect policy.
//
// There is no Environment: overlays are created for every environment the
// repository defines unless Environments narrows it. There is no AllowDirty, no
// NoWait and no Timeout either — no source working tree is read for correctness,
// and nothing is synced.
type manifestRunOptions struct {
	// Mode selects the destination. modeLocal renders for the local
	// development cluster instead of scaffolding the GitOps tree.
	Mode manifestMode

	// Namespace is the target namespace a local render emits. Empty defaults
	// to the system name. GitOps mode rejects it: the namespace there is the
	// templates' namespace/namespacePerEnv parameters.
	Namespace string

	// Images overrides image references in a local render, repeatable.
	// "<component>=<name:tag>" overrides one component's entry; ":<tag>"
	// (leading colon) retags every component — the release-candidate flow.
	// GitOps mode rejects them: `intropy deploy` pins digests, and scaffolding
	// never pins one.
	Images []string

	// Bindings supplies local port-to-fixture choices. Selector asks for
	// choices omitted from Bindings when terminal interaction is available.
	Bindings []string
	Selector interactive.Selector
	// Domain places the system in the GitOps tree. Unlike every other deploy
	// subcommand's --domain this is a destination rather than a filter. Empty is
	// inferred when exactly one domain already holds the system.
	Domain string

	// System names the system to scaffold.
	//
	// It does two things, which are the same thing said once: it selects which
	// system host to read the topology from when a workspace holds several, and
	// it is the tree segment. Empty means the only host in the workspace, named
	// by whatever its topology declares.
	System string

	// Environments selects which overlays to create. Empty means every
	// environment in deploy.yaml — and in local mode exactly the local
	// environment, whatever deploy.yaml says.
	Environments []string

	// TopologyFile reads the record from a file instead of running the host's
	// graph verb; "-" reads stdin. Internal test seam — not exposed as a CLI flag.
	TopologyFile string

	// SourceDir is where system hosts and scaffold records are discovered.
	// Internal test seam — not exposed as a CLI flag.
	SourceDir string

	// TemplateVersion pins the template library release.
	TemplateVersion string

	// Stdin is used only by the topology-file test seam. Manifest commands do
	// not prompt or accept ad-hoc value layers.
	Stdin io.Reader

	// PlanOnly resolves, renders and classifies, then reports without creating
	// manifest files, commits, or pushes.
	PlanOnly bool

	GitopsRepo   string
	OutputFormat string
	Color        bool
	CacheRoot    string

	Runner     command.Runner
	UserAgent  string
	CliVersion string
	Stdout     io.Writer
	Stderr     io.Writer

	// Owner and Repo select the template library; zero values target the
	// official library. Source carries the fetch seams (GitHubBaseURL
	// redirects the latest-release API call in tests).
	Owner  string
	Repo   string
	Source template.SourceOptions
	HTTP   *http.Client

	// The manifests create command sets these internal policy fields. Keeping
	// them private prevents another deploy operation from enabling create-only
	// behavior accidentally.
	diffOnly  bool
	reviewEnv string
}

func (o manifestRunOptions) output() output {
	return output{Format: o.OutputFormat, Color: o.Color, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o manifestRunOptions) session() sessionOptions {
	return sessionOptions{
		GitopsRepo: o.GitopsRepo,
		CacheRoot:  o.CacheRoot,
		Runner:     o.Runner,
		Stderr:     o.Stderr,
	}
}

func (o *manifestRunOptions) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
	}
	if o.SourceDir == "" {
		o.SourceDir = "."
	}
	if o.OutputFormat == "" {
		o.OutputFormat = OutputPlain
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
}

// renderLocalManifests resolves port bindings, stages the local tree,
// applies namespace and image overrides through a root kustomization, and
// returns the complete build. It never inspects or changes a cluster.
func renderLocalManifests(ctx context.Context, opts manifestRunOptions, found discoveredTopology, lib *template.Library) ([]byte, error) {
	overrides, err := parseImageOverrides(opts.Images)
	if err != nil {
		return nil, err
	}

	facts, err := resolveLocalFacts(opts, found)
	if err != nil {
		return nil, err
	}
	namespace := opts.Namespace
	if namespace == "" {
		namespace = facts.System
	}

	bindings, err := resolveLocalPortBindings(ctx, opts, facts, lib)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp("", "intropy-manifests-render-*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	fmt.Fprintf(opts.Stderr, "rendering %s for local\n", facts.System)
	if err := renderScaffold(ctx, opts, facts, bindings, lib, staging); err != nil {
		return nil, err
	}

	images, err := localImageEntries(facts, overrides)
	if err != nil {
		return nil, err
	}
	if err := writeLocalRootKustomization(staging, namespace, facts, images); err != nil {
		return nil, err
	}

	built, err := kustomizeBuild(ctx, opts.Runner, staging)
	if err != nil {
		return nil, err
	}
	if err := assertAllImagesTagged(built); err != nil {
		return nil, err
	}
	return built, nil
}

// initGitOps is the modeGitOps publish path: render the repo-relative staging
// tree, classify every file against the checkout, and push the result on a
// review branch.
func initGitOps(ctx context.Context, opts manifestRunOptions, found discoveredTopology, lib *template.Library) error {
	out := opts.output()

	s, err := openSession(ctx, opts.session(), "git")
	if err != nil {
		return err
	}
	defer s.Close()

	facts, err := resolveGitOpsFacts(opts, s, found)
	if err != nil {
		return err
	}

	bindings := map[string]map[string]string{}
	if len(facts.Model.Ports) > 0 {
		bindings, err = resolveGitOpsPortBindings(ctx, opts, facts, lib)
		if err != nil {
			return err
		}
	}

	staging, err := os.MkdirTemp("", "intropy-manifests-create-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	// Opened once, and every read of and write to the checkout goes through it:
	// the manifests come from a repository this CLI does not control, and the
	// tree segments come from flags.
	dest, err := openDestTree(s.repo.Root)
	if err != nil {
		return err
	}
	defer dest.Close()

	if err := renderScaffold(ctx, opts, facts, bindings, lib, staging); err != nil {
		return err
	}
	branch := manifestsCreateBranchPrefix + facts.Domain + "-" + facts.System + "-" + opts.reviewEnv

	rels, err := stageRels(staging)
	if err != nil {
		return err
	}
	actions, err := classifyStaged(staging, dest, rels)
	if err != nil {
		return err
	}
	if opts.diffOnly {
		return reportManifestCreateDiff(out.Stdout, staging, dest, actions)
	}
	if err := refuseManifestReplacements(actions); err != nil {
		return err
	}

	result := ManifestCreateResult{
		System:       facts.System,
		Domain:       facts.Domain,
		Host:         HostDirName,
		Template:     lib.Ref(),
		Components:   facts.ComponentNames(),
		Files:        actions,
		Applied:      false,
		Placeholders: nil,
	}

	if opts.PlanOnly {
		// Scan the staging tree: the files are not in the repository yet, so the
		// only place the placeholders exist is where they were rendered.
		result.Placeholders, err = scanPlaceholders(staging, rels)
		if err != nil {
			return err
		}
		return reportManifestCreate(out, result, actions, true)
	}

	if writes(actions) == 0 {
		// Nothing to write, but the placeholders already in the repository are
		// still the answer to "what is left to do here".
		result.Placeholders, err = scanPlaceholders(s.repo.Root, rels)
		if err != nil {
			return err
		}
		return reportManifestCreate(out, result, actions, false)
	}

	published, err := publishScaffold(ctx, opts, s.repo, publishScaffoldOptions{
		StagingDir: staging,
		Dest:       dest,
		Actions:    actions,
		Rels:       rels,
		Branch:     branch,
		Facts:      facts,
	})
	if err != nil {
		return err
	}
	result.Applied = true
	result.Branch = branch
	result.Revision = published.Revision
	result.Placeholders = published.Placeholders

	return reportManifestCreate(out, result, actions, false)
}

// resolveLocalFacts is resolveGitOpsFacts with local constants in place of the
// GitOps checkout's facts — newLocalFacts plus the selection and validation
// the GitOps path does on the way.
func resolveLocalFacts(opts manifestRunOptions, found discoveredTopology) (manifestFacts, error) {
	topo := found.Topology

	system := opts.System
	if system == "" {
		system = topo.System
	}
	if system == "" {
		return manifestFacts{}, fmt.Errorf("the topology record names no system; pass --system")
	}
	if err := assertPathSegment("--system", system); err != nil {
		return manifestFacts{}, err
	}

	model := newManifestModel(topo, found.Scaffolds)
	selected := slices.Clone(model.Components)
	for _, c := range selected {
		// From the topology record, which is generated by a build this CLI does
		// not own: a name with a separator in it would render into a directory of
		// its own choosing.
		if err := assertPathSegment("component name", c.Name); err != nil {
			return manifestFacts{}, err
		}
		if c.Dir == "" {
			fmt.Fprintf(opts.Stderr, "warning: %s has no scaffold record under the workspace root; its manifests will be generated without an appId or sourcePaths\n", c.Name)
		}
	}

	return newLocalFacts(system, model, matchScaffolds(topo.Components, found.Scaffolds), selected), nil
}

func writes(actions []FileAction) int {
	n := 0
	for _, a := range actions {
		if a.Writes() {
			n++
		}
	}
	return n
}

// discoveredTopology is what discoverManifestTopology found in the workspace.
type discoveredTopology struct {
	Topology  *topology.Topology
	Scaffolds []template.ScaffoldEntry

	// HostDir is the system host the record came from. Its path is the best
	// source for the domain.
	HostDir string
}

// discoverManifestTopology obtains the record and the workspace's scaffold records.
func discoverManifestTopology(ctx context.Context, opts manifestRunOptions) (discoveredTopology, error) {
	scaffolds, warnings := template.ListScaffolds(opts.SourceDir)
	for _, w := range warnings {
		fmt.Fprintf(opts.Stderr, "warning: %v\n", w)
	}

	if opts.TopologyFile != "" {
		r := opts.Stdin
		if opts.TopologyFile != "-" {
			f, err := os.Open(opts.TopologyFile)
			if err != nil {
				return discoveredTopology{}, fmt.Errorf("read topology: %w", err)
			}
			defer f.Close()
			r = f
		}
		topo, err := topology.Decode(r)
		if err != nil {
			return discoveredTopology{}, fmt.Errorf("read topology %s: %w", opts.TopologyFile, err)
		}
		return discoveredTopology{Topology: topo, Scaffolds: scaffolds}, nil
	}

	var hosts []template.ScaffoldEntry
	for _, s := range scaffolds {
		if s.Role == template.RoleSystemHost {
			hosts = append(hosts, s)
		}
	}
	host, err := selectHost(hosts, opts.System, opts.SourceDir)
	if err != nil {
		return discoveredTopology{}, err
	}

	// Said before starting, not after: a silent multi-minute wait reads as a hang.
	fmt.Fprintf(opts.Stderr, "reading topology from %s (building first; this can take a minute)\n", host.Path)
	topo, err := runGraph(ctx, host.Path)
	if err != nil {
		return discoveredTopology{}, fmt.Errorf("%s: %w", host.Path, err)
	}
	if opts.System != "" && topo.System != "" && opts.System != topo.System {
		// Not an error: --system names the system, and a caller who asked for a
		// different name gets it. Worth saying out loud, because it silently
		// decides the tree segment.
		fmt.Fprintf(opts.Stderr, "warning: the topology declares system %q; using %q as the tree segment\n", topo.System, opts.System)
	}
	return discoveredTopology{Topology: topo, Scaffolds: scaffolds, HostDir: host.Path}, nil
}

// selectHost picks which system host to read the topology from.
//
// Selection is by name and costs nothing: a scaffolded host records the system
// name in its own record, and its parent directory is the system directory. Both
// are on disk, so a multi-system workspace does not have to build every host to
// find out which one was meant.
//
// With no --system and one host, that host. With no --system and several, an
// error — picking one arbitrarily would scaffold the wrong system.
func selectHost(hosts []template.ScaffoldEntry, system, sourceDir string) (template.ScaffoldEntry, error) {
	if len(hosts) == 0 {
		return template.ScaffoldEntry{}, fmt.Errorf("no system host found under %s; run this from a system workspace", sourceDir)
	}

	if system != "" {
		var matches []template.ScaffoldEntry
		for _, h := range hosts {
			if matchesSystemName(h, system) {
				matches = append(matches, h)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			// A single host and no match is the older meaning of --system: rename
			// the tree segment rather than select anything. Still useful, and
			// unambiguous when there is only one system to talk about.
			if len(hosts) == 1 {
				return hosts[0], nil
			}
			return template.ScaffoldEntry{}, fmt.Errorf("no system host named %q under %s.\nFound:\n%s",
				system, sourceDir, describeHosts(hosts))
		default:
			return template.ScaffoldEntry{}, fmt.Errorf("%q matches %d system hosts, so it does not say which one:\n%s",
				system, len(matches), describeHosts(matches))
		}
	}

	if len(hosts) == 1 {
		return hosts[0], nil
	}
	return template.ScaffoldEntry{}, fmt.Errorf("%s holds %d system hosts; pass --system to pick one:\n%s",
		sourceDir, len(hosts), describeHosts(hosts))
}

// matchesSystemName reports whether a host is the one the caller named.
//
// Three keys are tried, in descending reliability: the record's own name (what
// sys create writes), the system directory holding the host, and the host
// directory itself — which is often a generic "system-host" and so the weakest.
func matchesSystemName(h template.ScaffoldEntry, system string) bool {
	want := normalizeSystemName(system)
	recordName, _ := template.SoftValue(h.Values, template.KeyName)
	keys := []string{
		recordName,
		filepath.Base(filepath.Dir(h.Path)),
		filepath.Base(h.Path),
	}
	for _, k := range keys {
		if k != "" && normalizeSystemName(k) == want {
			return true
		}
	}
	return false
}

// normalizeSystemName folds the spellings that mean the same system. sys create
// kebab-cases whatever it is given, so OrderFlow and order-flow are the same
// system and must select the same host.
func normalizeSystemName(s string) string {
	return strings.ToLower(xstrings.ToKebabCase(s))
}

func describeHosts(hosts []template.ScaffoldEntry) string {
	lines := make([]string, 0, len(hosts))
	for _, h := range hosts {
		name, _ := template.SoftValue(h.Values, template.KeyName)
		if name == "" {
			name = filepath.Base(filepath.Dir(h.Path))
		}
		lines = append(lines, fmt.Sprintf("  %s  (%s)", name, h.Path))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// manifestFacts is everything derived before rendering: where the manifests go, what
// the platform is, and the model the skeletons range over.
type manifestFacts struct {
	Domain string
	System string

	Environments []string
	Registry     string
	AppNamespace string
	Platform     gitops.PlatformConfig

	Model     ManifestModel
	Scaffolds map[string]template.ScaffoldEntry
	Selected  []ManifestComponent
}

func (f manifestFacts) ComponentNames() []string {
	names := make([]string, 0, len(f.Selected))
	for _, c := range f.Selected {
		names = append(names, c.Name)
	}
	return names
}

func resolveGitOpsFacts(opts manifestRunOptions, s *session, found discoveredTopology) (manifestFacts, error) {
	topo, scaffolds := found.Topology, found.Scaffolds

	system := opts.System
	if system == "" {
		system = topo.System
	}
	if system == "" {
		return manifestFacts{}, fmt.Errorf("the topology record names no system; pass --system")
	}

	domain, err := resolveManifestDomain(s.repo.Root, opts.Domain, system, found.HostDir, scaffolds, opts.Stderr)
	if err != nil {
		return manifestFacts{}, err
	}

	// Both become tree segments and both end up in the branch name, so neither
	// may be anything but a single name. destTree refuses a traversal at the
	// write; failing here instead names the input that was wrong.
	if err := assertPathSegment("--domain", domain); err != nil {
		return manifestFacts{}, err
	}
	if err := assertPathSegment("--system", system); err != nil {
		return manifestFacts{}, err
	}

	environments, err := selectEnvironments(s.deployCfg, opts.Environments)
	if err != nil {
		return manifestFacts{}, err
	}

	model := newManifestModel(topo, scaffolds)
	selected := slices.Clone(model.Components)
	for _, c := range selected {
		// From the topology record, which is generated by a build this CLI does
		// not own: a name with a separator in it would render into a directory of
		// its own choosing.
		if err := assertPathSegment("component name", c.Name); err != nil {
			return manifestFacts{}, err
		}
		if c.Dir == "" {
			fmt.Fprintf(opts.Stderr, "warning: %s has no scaffold record under the workspace root; its manifests will be generated without an appId or sourcePaths\n", c.Name)
		}
	}

	return manifestFacts{
		Domain:       domain,
		System:       system,
		Environments: environments,
		Registry:     s.deployCfg.Registry,
		AppNamespace: s.deployCfg.Argocd.AppNamespace,
		Platform:     s.deployCfg.Platform,
		Model:        model,
		Scaffolds:    matchScaffolds(topo.Components, scaffolds),
		Selected:     selected,
	}, nil
}

// resolveManifestDomain places the system in the tree.
//
// The topology itself says nothing about which business domain a system belongs
// to, but two other things do, and between them --domain is rarely needed:
//
//   - where the system already is in the GitOps tree, which is authoritative —
//     a re-run must land where the component actually lives;
//   - the source workspace's own layout, which every customer integrations tree
//     mirrors from the deployment tree: domains/<domain>/<system>/<project>.
//
// An explicit --domain always wins, and is still required when neither holds.
func resolveManifestDomain(root, flag, system, hostDir string, scaffolds []template.ScaffoldEntry, stderr io.Writer) (string, error) {
	if flag != "" {
		return flag, nil
	}

	inTree := domainsHoldingSystem(root, system)
	fromWorkspace := domainFromWorkspaceLayout(hostDir, scaffolds)

	switch len(inTree) {
	case 1:
		// The repository wins over the workspace: moving a system between domains
		// is a deliberate act, not something a directory name should trigger.
		if fromWorkspace != "" && fromWorkspace != inTree[0] {
			fmt.Fprintf(stderr, "warning: %s is already under %s/%s in the GitOps repository, but the workspace suggests %q; keeping %s — pass --domain to move it deliberately\n",
				system, gitops.DomainsDirName, inTree[0], fromWorkspace, inTree[0])
		}
		return inTree[0], nil
	case 0:
		if fromWorkspace != "" {
			fmt.Fprintf(stderr, "placing %s under %s/%s, from the workspace layout\n", system, gitops.DomainsDirName, fromWorkspace)
			return fromWorkspace, nil
		}
		return "", fmt.Errorf("--domain is required: %s is not in the GitOps repository yet, and the workspace is not laid out as %s/<domain>/%s/, so there is no domain to infer",
			system, gitops.DomainsDirName, system)
	default:
		return "", fmt.Errorf("--domain is required: %s exists under %s", system, strings.Join(inTree, " and "))
	}
}

// domainsHoldingSystem lists the domains the GitOps tree already files this
// system under, sorted. More than one means the name is ambiguous.
func domainsHoldingSystem(root, system string) []string {
	var found []string
	for _, c := range gitops.ListComponents(root) {
		if c.System == system && !slices.Contains(found, c.Domain) {
			found = append(found, c.Domain)
		}
	}
	slices.Sort(found)
	return found
}

// domainFromWorkspaceLayout reads the domain out of the source tree's own shape.
//
// The system host is tried first because it is the system's own directory; a
// block is only a fallback for the --topology case, where no host was discovered.
func domainFromWorkspaceLayout(hostDir string, scaffolds []template.ScaffoldEntry) string {
	if d := domainFromProjectPath(hostDir); d != "" {
		return d
	}
	for _, s := range scaffolds {
		if d := domainFromProjectPath(s.Path); d != "" {
			return d
		}
	}
	return ""
}

// domainFromProjectPath returns the <domain> in
// <...>/domains/<domain>/<system>/<project>, or empty when the path is not laid
// out that way.
//
// The marker directory has to actually be there — gitops.DomainsDirName, the same
// constant the deployment tree uses. Without it nothing is inferred, so a
// workspace with some other shape gets a clear "--domain is required" rather than
// a guess.
func domainFromProjectPath(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	domainDir := filepath.Dir(filepath.Dir(abs))
	if filepath.Base(filepath.Dir(domainDir)) != gitops.DomainsDirName {
		return ""
	}
	name := filepath.Base(domainDir)
	if name == "." || name == string(filepath.Separator) || name == gitops.DomainsDirName {
		return ""
	}
	return name
}

func selectEnvironments(cfg *gitops.DeployConfig, requested []string) ([]string, error) {
	all := len(requested) == 0
	if all {
		// Every environment, in promotion order. The ApplicationSet takes the
		// environment from its cluster generator rather than the path, so an
		// environment without an overlay is an Application pointing at nothing.
		requested = cfg.PromotionOrder()
	}
	for _, env := range requested {
		if _, err := cfg.Environment(env); err != nil {
			return nil, err
		}
		if err := assertPathSegment("environment name", env); err != nil {
			return nil, err
		}
	}
	out := slices.Clone(requested)
	if all {
		return out, nil
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// renderScaffold renders the host template once and the component template once
// per selected component.
//
// In GitOps mode staging mirrors the repository, so a staged path is already
// the repo-relative path the classifier and the commit both need. A local
// render stages flat — host/ and one directory per component — because the
// temp root already holds exactly one system and no commit needs a
// repo-relative path.
func renderScaffold(ctx context.Context, opts manifestRunOptions, facts manifestFacts, bindings map[string]map[string]string, lib *template.Library, staging string) error {
	if err := renderOne(opts, facts, bindings, lib, TemplateDeployHost, HostDirName, nil, staging); err != nil {
		return err
	}
	for _, c := range facts.Selected {
		comp := c
		if err := renderOne(opts, facts, bindings, lib, TemplateDeployComponent, c.Name, &comp, staging); err != nil {
			return err
		}
	}
	return nil
}

// renderOne renders one unit — the host, or one component — into the staging
// tree.
//
// The skeleton is rendered once per environment, because the renderer templates
// paths but does not iterate them, so `overlays/{{ .env }}/kustomization.yaml` is
// the only way to get one directory per environment. mergeRendered then enforces
// that everything outside overlays/ came out identical on every pass.
func renderOne(opts manifestRunOptions, facts manifestFacts, bindings map[string]map[string]string, lib *template.Library, templateName, dirName string, comp *ManifestComponent, staging string) error {
	tmpl, skeleton, err := lib.Open(templateName)
	if err != nil {
		return err
	}

	// GitOps mode stages the repository-relative path; a local render is flat.
	dest := filepath.Join(staging, dirName)
	if opts.Mode != modeLocal {
		dest = filepath.Join(staging, filepath.FromSlash(componentRelPath(facts.Domain, facts.System, dirName)))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	rules := tmpl.Spec.Files
	if opts.Mode == modeLocal {
		rules = localFileRules(rules)
	} else {
		rules = gitOpsFileRules(rules)
	}
	for _, env := range facts.Environments {
		values, err := resolveManifestValues(opts, facts, bindings, tmpl, dirName, comp, env)
		if err != nil {
			return fmt.Errorf("%s for %s: %w", templateName, dirName, err)
		}

		pass, err := os.MkdirTemp("", "intropy-render-*")
		if err != nil {
			return err
		}
		err = template.RenderFiltered(skeleton, pass, values, rules)
		if err == nil {
			err = mergeRendered(pass, dest, env)
		}
		os.RemoveAll(pass)
		if err != nil {
			return fmt.Errorf("render %s for %s (%s): %w", templateName, dirName, env, err)
		}
	}
	return nil
}

// resolveManifestValues layers the derived facts under the user's input, then injects
// the structures spec.parameters cannot describe.
//
// Facts beat schema defaults; the user beats facts. That order is the whole point
// of ResolveLayered, and this is its first production caller.
func resolveManifestValues(opts manifestRunOptions, facts manifestFacts, bindings map[string]map[string]string, tmpl *template.Template, dirName string, comp *ManifestComponent, env string) (map[string]any, error) {
	base := seedDeclaredParams(tmpl, manifestSeeds(facts, dirName, comp))

	prompter := template.AutoPrompter(opts.Stdin, opts.Stderr, true)
	values, err := template.ResolveLayered(tmpl, base, nil, opts.Stdin, nil, prompter)
	if err != nil {
		return nil, err
	}

	model, err := facts.Model.asMap(env, bindings)
	if err != nil {
		return nil, err
	}
	reserved := map[string]any{
		template.ReservedEnvKey:      env,
		template.ReservedTopologyKey: model,
		template.ReservedGitopsKey:   facts.gitopsMap(dirName, env),
	}
	if env == localEnv {
		// Deprecated: the ports' binding field carries the same fact. Kept
		// so a library older than that field still renders its fixture overlay;
		// remove when the floor template version has moved past it.
		local, err := toMap(localModel{Bindings: bindingsForEnv(facts.Model, bindings, env)})
		if err != nil {
			return nil, err
		}
		reserved[template.ReservedLocalKey] = local
	}
	if comp != nil {
		componentMap, err := toMap(comp)
		if err != nil {
			return nil, err
		}
		reserved[template.ReservedComponentKey] = componentMap
		reserved[template.ReservedScaffoldKey] = facts.scaffoldValues(comp.Name)
	}
	if err := template.InjectReserved(tmpl, values, reserved); err != nil {
		return nil, err
	}
	return values, nil
}

// manifestSeeds is every fact the CLI can derive, keyed by the parameter name a
// template would declare for it.
func manifestSeeds(facts manifestFacts, dirName string, comp *ManifestComponent) map[string]any {
	seeds := map[string]any{
		"domain":             facts.Domain,
		"system":             facts.System,
		"hostName":           HostDirName,
		"registry":           facts.Registry,
		"argocdAppNamespace": facts.AppNamespace,
		"provider":           facts.Platform.Provider,
		"pubsub":             facts.Platform.Pubsub,
		"secretStore":        facts.Platform.SecretStore,
	}
	// Only when unambiguous. Guessing between two brokers would put the wrong
	// name in a Component that the generated C# then fails to resolve.
	if len(facts.Model.PubSubs) == 1 {
		seeds["pubsubName"] = facts.Model.PubSubs[0].Name
	}
	if comp != nil {
		seeds["name"] = comp.Name
		seeds["kind"] = comp.Kind
		seeds["appId"] = comp.AppID
		seeds["workload"] = comp.Workload
		if comp.Dir != "" {
			seeds["sourceDir"] = comp.Dir
		}
	} else {
		seeds["name"] = dirName
		seeds["appId"] = dirName
	}
	return seeds
}

// seedDeclaredParams keeps only the seeds the template declares as parameters.
//
// An undeclared key would sail past JSON Schema validation and land in the value
// map anyway, which is how a template silently acquires a dependency nobody
// documented.
func seedDeclaredParams(tmpl *template.Template, seeds map[string]any) map[string]any {
	base := make(map[string]any, len(seeds))
	for _, f := range tmpl.Fields() {
		if v, ok := seeds[f.Name]; ok && !isBlank(v) {
			base[f.Name] = v
		}
	}
	return base
}

// isBlank drops an empty derived fact so the template's own default still
// applies: an unset platform.pubsub must not override `default: rabbitmq`.
func isBlank(v any) bool {
	s, ok := v.(string)
	return ok && s == ""
}

func (f manifestFacts) gitopsMap(component, env string) map[string]any {
	return map[string]any{
		"domain":             f.Domain,
		"system":             f.System,
		"component":          component,
		"env":                env,
		"host":               HostDirName,
		"registry":           f.Registry,
		"argocdAppNamespace": f.AppNamespace,
		"environments":       f.Environments,
		"platform": map[string]any{
			"provider":    f.Platform.Provider,
			"pubsub":      f.Platform.Pubsub,
			"secretStore": f.Platform.SecretStore,
		},
	}
}

// scaffoldValues is the component's recorded template values, or an empty map so
// a skeleton reading .scaffold.x never fails on a missing record.
func (f manifestFacts) scaffoldValues(component string) map[string]any {
	if s, ok := f.Scaffolds[component]; ok && s.Values != nil {
		return s.Values
	}
	return map[string]any{}
}

func toMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type publishScaffoldOptions struct {
	StagingDir string
	Dest       *destTree
	Actions    []FileAction
	Rels       []string
	Branch     string
	Facts      manifestFacts
}

// publishedScaffold is what a successful push produced.
type publishedScaffold struct {
	Revision string

	// Placeholders are scanned from the working tree *while the branch is still
	// checked out*. Scanning afterwards would find nothing: restoring the default
	// branch takes the new files back out of the working tree.
	Placeholders []Placeholder
}

// publishScaffold writes the staged files on a fresh branch and pushes it.
//
// Never to the default branch: the tree is full of placeholders and unpinned
// images, and the ApplicationSet would generate Applications for it the moment it
// landed. A branch is the reviewable artefact.
//
// Publish is not reused: it is wired to a Plan and its rebase-retry loop is for a
// one-line contended edit. A fresh branch that is rejected already exists on the
// remote, which is a clear error rather than something to retry.
func publishScaffold(ctx context.Context, opts manifestRunOptions, repo *gitops.Repository, p publishScaffoldOptions) (publishedScaffold, error) {
	start := gitops.RemoteName + "/" + repo.Branch
	if err := repo.Git.CreateBranch(ctx, p.Branch, start); err != nil {
		return publishedScaffold{}, err
	}
	// Unconditionally, on success and on failure alike: the refresh on the next
	// Open resets whatever branch is current to the default branch's remote head,
	// so leaving the checkout here would silently discard these commits.
	defer func() {
		if err := repo.Git.Switch(ctx, repo.Branch); err != nil {
			fmt.Fprintf(opts.Stderr, "warning: could not restore %s in the cached checkout: %v\n", repo.Branch, err)
		}
	}()

	written, err := applyStaged(p.StagingDir, p.Dest, p.Actions)
	if err != nil {
		return publishedScaffold{}, err
	}
	// Only the paths staged, never `add -A`: the checkout is shared, and a
	// concurrent run's debris is not ours to commit.
	if err := repo.Git.Add(ctx, written...); err != nil {
		return publishedScaffold{}, err
	}
	// The scaffold is reviewed as a branch, and a reviewer reads the file list as
	// the whole of what this command did. It has to actually be that.
	if err := assertStagedIsExactly(ctx, repo.Git, written); err != nil {
		return publishedScaffold{}, err
	}
	subject := manifestCreateSubject(p.Facts, opts.reviewEnv, len(written))
	if err := repo.Git.Commit(ctx, subject, scaffoldTrailers(p.Facts, opts.reviewEnv, opts.CliVersion)); err != nil {
		return publishedScaffold{}, err
	}
	if err := assertCommittedIsExactly(ctx, repo.Git, written); err != nil {
		return publishedScaffold{}, err
	}
	if err := repo.Git.Push(ctx, gitops.RemoteName, "HEAD:refs/heads/"+p.Branch); err != nil {
		return publishedScaffold{}, fmt.Errorf("push %s: %w\n\nA rejection here usually means the branch already exists on the remote. Delete it, or merge the open review first", p.Branch, err)
	}

	revision, err := repo.Git.HEAD(ctx)
	if err != nil {
		return publishedScaffold{}, err
	}
	// While the branch is still current — the deferred switch above takes these
	// files back out of the working tree.
	placeholders, err := scanPlaceholders(repo.Root, p.Rels)
	if err != nil {
		return publishedScaffold{}, err
	}
	return publishedScaffold{Revision: revision, Placeholders: placeholders}, nil
}

func manifestCreateSubject(facts manifestFacts, env string, files int) string {
	return fmt.Sprintf("manifests(%s): create %d %s file%s under %s", facts.System, files, env, plural(files), facts.Domain)
}

func scaffoldTrailers(facts manifestFacts, env, cliVersion string) string {
	trailers := []git.Trailer{
		{Key: TrailerDomain, Value: facts.Domain},
		{Key: TrailerSystem, Value: facts.System},
	}
	if env != "" {
		trailers = append(trailers, git.Trailer{Key: TrailerEnvironment, Value: env})
	}
	for _, c := range facts.Selected {
		trailers = append(trailers, git.Trailer{Key: TrailerComponent, Value: c.Name})
	}
	if who := committer(); who != "" {
		trailers = append(trailers, git.Trailer{Key: TrailerBy, Value: who})
	}
	if cliVersion != "" {
		trailers = append(trailers, git.Trailer{Key: TrailerCli, Value: cliVersion})
	}

	lines := make([]string, 0, len(trailers))
	for _, t := range trailers {
		lines = append(lines, t.String())
	}
	return strings.Join(lines, "\n")
}

// reportManifestCreate writes the action table, then what remains to be done.
func reportManifestCreate(out output, result ManifestCreateResult, actions []FileAction, planOnly bool) error {
	if out.Format == OutputJSON {
		enc := json.NewEncoder(out.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("write JSON result: %w", err)
		}
		return nil
	}

	for _, a := range actions {
		fmt.Fprintf(out.Stdout, "  %-12s %s\n", a.Action, a.Rel)
	}
	fmt.Fprintf(out.Stdout, "\n%s\n", summariseActions(actions))

	switch {
	case planOnly:
		fmt.Fprintln(out.Stdout, "nothing created (--dry-run)")
	case result.Applied:
		fmt.Fprintf(out.Stdout, "pushed branch %s at %s\n", result.Branch, git.ShortSHA(result.Revision))
	default:
		fmt.Fprintln(out.Stdout, "all manifest files already exist; nothing created")
	}

	reportPlaceholders(out, result.Placeholders)
	return nil
}

func conflictingPaths(actions []FileAction) []string {
	var out []string
	for _, a := range actions {
		if a.Action == ActionConflict {
			out = append(out, a.Rel)
		}
	}
	return out
}

func refuseManifestReplacements(actions []FileAction) error {
	conflicts := conflictingPaths(actions)
	if len(conflicts) == 0 {
		return nil
	}
	var lines []string
	for _, rel := range conflicts {
		lines = append(lines, "  "+rel)
	}
	return fmt.Errorf("%d manifest file%s already exist and differ:\n%s\nmanifests create never replaces files; edit the existing GitOps files or remove them before creating again",
		len(conflicts), plural(len(conflicts)), strings.Join(lines, "\n"))
}

func reportManifestCreateDiff(w io.Writer, staging string, dest *destTree, actions []FileAction) error {
	for _, action := range actions {
		if action.Action == ActionIdentical {
			continue
		}
		after, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(action.Rel)))
		if err != nil {
			return fmt.Errorf("read staged %s: %w", action.Rel, err)
		}
		before, err := dest.read(action.Rel)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read %s: %w", action.Rel, err)
		}
		diff := kustomize.Diff(before, after, action.Rel+" (current)", action.Rel+" (generated)", kustomize.PlainPalette)
		if diff != "" {
			fmt.Fprint(w, diff)
		}
	}
	return nil
}
