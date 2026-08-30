package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// maxDocBytes caps how much of an enrichment document (AGENTS.md, README.md)
// the detail endpoint returns, so a huge file cannot bloat a response.
const maxDocBytes = 128 * 1024

// providers are the request-independent sources the API reads beyond the
// workspace's own files: what a system host declares about its topology, and
// what the GitOps repository says is deployed. Both run a command, so both are
// injected as function values that tests replace. templates is a value, not a
// function: it carries configuration (release pin, test overrides), not a
// stubbed behavior — tests override its fields instead.
type providers struct {
	topology  topologyProvider
	deploy    deployProvider
	templates templatesProvider
}

// deployStateTimeout bounds one deploy status run.
//
// It exists for the checkout lock rather than for the browser: the command holds
// an exclusive lock on the shared GitOps checkout for its whole duration, and an
// ArgoCD server that accepts connections but never answers would otherwise keep
// that lock — blocking the user's own intropy deploy indefinitely. A minute is
// generous for a fetch plus a handful of ArgoCD calls and short enough that a
// hung one lets go.
const deployStateTimeout = time.Minute

// apiServer holds the request-independent state for the JSON API.
type apiServer struct {
	root      string
	version   string
	topo      topologyProvider
	dep       deployProvider
	templates templatesProvider

	// organization is the resolved config's customer, seeded into
	// workspace facts as the ambient organization default when the
	// workspace's own records name none.
	organization string

	// createMu serializes template create runs: two concurrent renders of the
	// same name would race on the output directory, and each run downloads a
	// library tarball. The same bargain depMu makes for the shared checkout.
	createMu sync.Mutex

	// topoMu guards the cached provider result. Fetching runs the hosts'
	// graph verbs (a dotnet build on first run), so the result is computed
	// once — on the first /api/topology request — and reused until an
	// explicit refresh.
	topoMu      sync.Mutex
	topoLoaded  bool
	topoWarming bool
	topoEntries []topology.Entry
	topoErrs    []string

	// depMu guards the cached deployment state, keyed by root-relative path.
	// Reading it refreshes a shared GitOps checkout under an exclusive lock, so
	// unlike the topology this is cached per integration and computed only for
	// the ones actually asked about.
	depMu     sync.Mutex
	depStates map[string]deployState

	// runMu guards the supervised system hosts, keyed by root-relative system
	// dir. start launches through the start function value (dotnet run in
	// production, a fake in tests — the same seam providers uses).
	runMu sync.Mutex
	runs  map[string]*systemRun
	start starter
}

// topologyReport is the /api/topology payload: every declared topology plus
// the per-host failures the UI renders. Topologies is always an array (never
// null) even when empty.
type topologyReport struct {
	Topologies []topologyEntry `json:"topologies"`
	Errors     []string        `json:"errors,omitempty"`
}

// topologyEntry is one system's declared topology plus the authored
// enrichment read from its directory: message descriptions keyed by port
// name. The docs ride beside the Topology rather than inside it — the
// topology stays exactly what the host declared.
type topologyEntry struct {
	topology.Entry
	MessageDocs map[string]messageDoc `json:"messageDocs,omitempty"`
}

// fileDoc is a text document surfaced in an integration's detail view.
type fileDoc struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// integrationSummary is a scaffold entry plus the coordinates the sidebar
// tree groups by: Name is the directory that carries the .intropy record.
// System is the name a sibling system-host scaffold declares for the
// directory's parent, falling back to the parent folder's name; Domain is the
// system's parent folder. Absent a host, System is empty when the integration
// sits directly under the workspace root, and Domain is empty when the
// system does.
type integrationSummary struct {
	template.ScaffoldEntry
	Name   string `json:"name"`
	System string `json:"system,omitempty"`
	Domain string `json:"domain,omitempty"`
	// SystemPath is the root-relative directory of the system the integration
	// belongs to ("." when the system is rooted at the workspace itself).
	// Unlike System — a declared name two directories can share — it is a
	// unique grouping key, and it matches the `path` of the topology record
	// the system's host produces. Absent when the integration has no system.
	SystemPath string `json:"systemPath,omitempty"`
}

// integrationDetail is a single integration plus best-effort, on-disk
// enrichment. Every enrichment field is optional and absent when the
// corresponding file or directory does not exist.
type integrationDetail struct {
	integrationSummary
	Agents        *fileDoc        `json:"agents,omitempty"`
	Readme        *fileDoc        `json:"readme,omitempty"`
	Components    []DaprComponent `json:"components,omitempty"`
	PipelineSteps []string        `json:"pipelineSteps,omitempty"`
}

// newHandler wires the API routes and the SPA static handler onto a mux and
// returns the apiServer alongside: Serve needs its shutdownRuns (stopping the
// supervised system hosts after the HTTP server drains) and tests need its
// start seam.
func newHandler(root, version string, p providers) (http.Handler, *apiServer, error) {
	api := &apiServer{root: root, version: version, topo: p.topology, dep: p.deploy, templates: p.templates, start: dotnetStart}
	static, err := staticHandler()
	if err != nil {
		return nil, nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", api.health)
	mux.HandleFunc("GET /api/integrations", api.listIntegrations)
	mux.HandleFunc("GET /api/systems", api.listSystems)
	// GET lists what is declared; POST re-assembles one system's host
	// (sys update, or sys create when the directory has none).
	mux.HandleFunc("POST /api/systems/{path...}", api.syncSystem)
	mux.HandleFunc("GET /api/integrations/{path...}", api.getIntegration)
	mux.HandleFunc("GET /api/catalog/{path...}", api.catalog)
	mux.HandleFunc("GET /api/flow", api.flow)
	mux.HandleFunc("GET /api/topology", api.topologies)
	mux.HandleFunc("POST /api/topology/refresh", api.refreshTopologies)
	// The verb carries the meaning rather than a /refresh segment: a {path...}
	// wildcard has to be the last thing in the pattern. GET serves what has
	// already been read, POST runs the command again.
	mux.HandleFunc("GET /api/deploy/{path...}", api.getDeployState)
	mux.HandleFunc("POST /api/deploy/{path...}", api.refreshDeployState)
	// Run supervision: the flow view's start/stop for a system's host. A
	// dedicated prefix — ServeMux forbids a {path...} wildcard mid-pattern, so
	// /api/systems/{path...}/run cannot exist. The workspace-root system
	// arrives as "" (ServeMux redirects /api/run/. to /api/run/) and the
	// handlers normalize it to ".", the same rule byPath applies.
	mux.HandleFunc("GET /api/run/{path...}", api.getRun)
	mux.HandleFunc("POST /api/run/{path...}", api.startRun)
	mux.HandleFunc("DELETE /api/run/{path...}", api.stopRun)
	// Template endpoints follow the same GET-read / POST-act split: list and
	// show fetch the library release, create renders into the workspace.
	mux.HandleFunc("GET /api/templates", api.listTemplates)
	mux.HandleFunc("GET /api/templates/{name}", api.getTemplate)
	// Registered before {name} so "suggestions" binds to the literal.
	mux.HandleFunc("GET /api/templates/suggestions/{name}", api.getTemplateSuggestions)
	mux.HandleFunc("POST /api/templates/{name}/create", api.createTemplate)
	// Test-file seeding: GET lists one system's testdata/<port>/ library,
	// POST copies a chosen file into the port's dev inbox. The GET's
	// {path...} wildcard is the root-relative system path.
	mux.HandleFunc("GET /api/testdata/{path...}", api.listTestData)
	mux.HandleFunc("POST /api/seed", api.seedTestFile)
	mux.Handle("/", static)
	return mux, api, nil
}

func (s *apiServer) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"version":   s.version,
		"workspace": s.workspaceName(),
	})
}

// workspaceName is the served root's directory name — the label the flow
// view gives the workspace pseudo-system, so a dashboard on ~/dev/acme says
// "acme" rather than a generic "Workspace". Serve hands the handler an
// absolute root; the extra Abs covers callers (tests) that pass a relative
// one, where Base would otherwise answer ".".
func (s *apiServer) workspaceName() string {
	root := s.root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Base(root)
}

// listIntegrations mirrors `int list -o json` — the same scaffold entries in
// the same order — with the derived name/system fields added on top and each
// Path normalized to a root-relative identifier. The response is always an
// array (never null) even when empty.
func (s *apiServer) listIntegrations(w http.ResponseWriter, _ *http.Request) {
	entries, systems := s.scan()
	summaries := make([]integrationSummary, 0, len(entries))
	for _, e := range entries {
		summaries = append(summaries, s.summarize(e, systems))
	}
	writeJSON(w, http.StatusOK, summaries)
}

// systemInfo is one declared system: the directory holding a system-host
// scaffold, root-relative, plus the name the host declares.
type systemInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// listSystems reports every declared system, so the flow view can offer a
// system that has a host but no blocks yet — /api/flow only carries systems
// through the blocks that belong to them. The response is always an array
// (never null) even when empty.
func (s *apiServer) listSystems(w http.ResponseWriter, _ *http.Request) {
	_, systems := s.scan()
	out := make([]systemInfo, 0, len(systems))
	for dir, name := range systems {
		out = append(out, systemInfo{Path: s.relPath(dir), Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, http.StatusOK, out)
}

// scan splits the workspace's scaffold entries for the API: the integration
// blocks to catalog, plus the declared systems keyed by their root directory
// (the directory holding a system-host scaffold). Support projects — the host
// and its shared contracts library — are infrastructure the system view
// already represents, not integrations to list.
func (s *apiServer) scan() (blocks []template.ScaffoldEntry, systems map[string]string) {
	entries, _ := template.ListScaffolds(s.root)
	systems = map[string]string{}
	for _, e := range entries {
		if e.Role == template.RoleSystemHost {
			systems[filepath.Clean(filepath.Dir(e.Path))] = systemName(e)
		}
	}
	blocks = entries[:0]
	for _, e := range entries {
		if e.Role == template.RoleSystemHost || e.Role == template.RoleSharedLibrary {
			continue
		}
		blocks = append(blocks, e)
	}
	return blocks, systems
}

// systemName is the name a system-host scaffold declares for its system
// (the template's `name` value, recorded at `sys create`), falling back to
// the host's parent directory name.
func systemName(host template.ScaffoldEntry) string {
	if name, ok := template.SoftValue(host.Values, template.KeyName); ok {
		return name
	}
	dir := filepath.Dir(host.Path)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Base(dir)
}

// summarize derives an entry's tree coordinates from its path and the
// declared systems. A sibling system-host makes membership explicit: its
// declared name wins over the folder-derived one and applies even directly
// under the workspace root (where no folder-derived system exists).
// Otherwise the workspace root itself never counts as a system or domain, so
// an integration scaffolded at the root has neither and one directly under
// the root has a system but no domain. The served Path is made root-relative
// so it is a stable, URL-safe identifier regardless of whether the command
// was given a relative or absolute root (an absolute path would otherwise
// break detail lookups).
func (s *apiServer) summarize(e template.ScaffoldEntry, systems map[string]string) integrationSummary {
	sum := integrationSummary{ScaffoldEntry: e, Name: filepath.Base(e.Path)}
	sum.Path = s.relPath(e.Path)
	path, root := filepath.Clean(e.Path), filepath.Clean(s.root)
	parent := filepath.Dir(path)
	if declared, ok := systems[parent]; ok && path != root {
		sum.System = declared
		sum.SystemPath = s.relPath(parent)
		if grand := filepath.Dir(parent); parent != root && grand != root {
			sum.Domain = filepath.Base(grand)
		}
		return sum
	}
	if path == root || parent == root {
		return sum
	}
	sum.System = filepath.Base(parent)
	sum.SystemPath = s.relPath(parent)
	if grand := filepath.Dir(parent); grand != root {
		sum.Domain = filepath.Base(grand)
	}
	return sum
}

// relPath turns an entry's on-disk path into the root-relative, slash-separated
// identifier used in API responses and URLs. It falls back to the cleaned path
// so a value is always produced even if Rel fails (e.g. differing volumes).
func (s *apiServer) relPath(p string) string {
	if rel, err := filepath.Rel(s.root, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// flow returns every integration enriched with the pipeline steps and Dapr
// components the flow canvas draws, so the whole graph loads in one request.
// It reuses integrationDetail but leaves the document fields (agents/readme)
// nil — the canvas nodes do not render them.
func (s *apiServer) flow(w http.ResponseWriter, _ *http.Request) {
	entries, systems := s.scan()
	nodes := make([]integrationDetail, 0, len(entries))
	for _, e := range entries {
		dir := e.Path
		nodes = append(nodes, integrationDetail{
			integrationSummary: s.summarize(e, systems),
			Components:         readComponents(filepath.Join(dir, "local", "dapr-components")),
			PipelineSteps:      listNames(filepath.Join(dir, "src", "Process", "Steps")),
		})
	}
	writeJSON(w, http.StatusOK, nodes)
}

// topologies returns every system topology declared by a host's graph verb,
// each Path normalized to the same root-relative identifier integration
// paths use, so the flow canvas can match a topology to the integrations
// living beside its host. The provider result is cached after the first
// request (the verb builds the host); POST /api/topology/refresh recomputes.
func (s *apiServer) topologies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.topologyReport(r.Context(), false))
}

// refreshTopologies recomputes the topology cache by re-running every host's
// graph verb, then returns the fresh report.
func (s *apiServer) refreshTopologies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.topologyReport(r.Context(), true))
}

// topologyReport assembles the /api/topology payload: the cached provider
// result plus the message docs read fresh from disk on every request, so an
// edited doc shows up on a browser refresh without re-running the hosts'
// graph verbs.
func (s *apiServer) topologyReport(ctx context.Context, force bool) topologyReport {
	entries, errs := s.cachedTopologies(ctx, force)
	report := topologyReport{
		Topologies: make([]topologyEntry, 0, len(entries)),
		Errors:     append([]string(nil), errs...),
	}
	for _, e := range entries {
		docs, docErrs := s.messageDocs(e)
		report.Topologies = append(report.Topologies, topologyEntry{Entry: e, MessageDocs: docs})
		report.Errors = append(report.Errors, docErrs...)
	}
	return report
}

// cachedTopologies returns the provider result — computed on first use or
// when force is set, reused otherwise. Concurrent requests share one
// computation. Entry paths are normalized to root-relative identifiers.
func (s *apiServer) cachedTopologies(ctx context.Context, force bool) ([]topology.Entry, []string) {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	if !s.topoLoaded || force {
		entries, errs := s.topo(ctx)
		s.topoEntries = make([]topology.Entry, 0, len(entries))
		for _, e := range entries {
			e.Path = s.relPath(e.Path)
			s.topoEntries = append(s.topoEntries, e)
		}
		s.topoErrs, s.topoLoaded = errs, true
	}
	return s.topoEntries, s.topoErrs
}

// topologiesLoaded reports whether the topology cache has been computed, and
// serves the cached result if it has. It never blocks on the computation:
// computing runs every host's graph verb (a dotnet build on first run) under
// one mutex, and the catalog endpoint must answer promptly rather than queue
// behind that. Instead the caller asks warmTopologies to compute in the
// background, so the pending answer becomes a matched one on the next fetch
// without anyone visiting the flow view first.
func (s *apiServer) topologiesLoaded() (loaded bool, entries []topology.Entry, errs []string) {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	return s.topoLoaded, s.topoEntries, s.topoErrs
}

// warmTopologies triggers the topology computation in the background when the
// cache is cold. Only one warm-up runs at a time — cachedTopologies
// serialises on topoMu, and warming guards against stacking requests behind
// it. A detached context keeps the hosts' graph verbs running after the
// request that prompted them has been answered.
func (s *apiServer) warmTopologies() {
	s.topoMu.Lock()
	if s.topoLoaded || s.topoWarming {
		s.topoMu.Unlock()
		return
	}
	s.topoWarming = true
	s.topoMu.Unlock()

	go func() {
		defer func() {
			s.topoMu.Lock()
			s.topoWarming = false
			s.topoMu.Unlock()
		}()
		// The catalog requests that triggered this are already answered; the
		// computation is for the next one.
		_, _ = s.cachedTopologies(context.Background(), false)
	}()
}

// messageDocs reads the authored port payload descriptions beside a system,
// keyed by port name. A doc naming a port the topology does not declare is
// surfaced as an error and withheld — never guessed around. Error messages
// carry the doc's workspace-relative path.
func (s *apiServer) messageDocs(e topology.Entry) (map[string]messageDoc, []string) {
	docs, errs := readMessageDocs(filepath.Join(s.root, filepath.FromSlash(e.Path)))
	prefix := ""
	if e.Path != "." {
		prefix = e.Path + "/"
	}
	for i, msg := range errs {
		errs[i] = prefix + msg
	}
	known := map[string]bool{}
	for _, c := range e.Ports {
		known[c.Name] = true
	}
	var unknown []string
	for name := range docs {
		if !known[name] {
			unknown = append(unknown, name)
			delete(docs, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		errs = append(errs, fmt.Sprintf("%s%s/%s.md: no port %q declared by system %q",
			prefix, messagesDirName, name, name, e.System))
	}
	if len(docs) == 0 {
		docs = nil
	}
	return docs, errs
}

func (s *apiServer) getIntegration(w http.ResponseWriter, r *http.Request) {
	s.byPath(w, r, func(e template.ScaffoldEntry, systems map[string]string) any {
		return s.enrich(e, systems)
	})
}

// getDeployState serves what the deploy status command last said about one
// integration, running it on the first request and reusing that until a POST to
// the same path. It is a separate endpoint from the integration detail because
// the two cost very different things: detail is local files, this refreshes a
// GitOps checkout over the network. Keeping them apart lets the detail panel
// render immediately and fill the deployment in when it arrives.
func (s *apiServer) getDeployState(w http.ResponseWriter, r *http.Request) {
	s.deployStateByPath(w, r, false)
}

// refreshDeployState re-runs the command for one integration and returns the
// fresh result, picking up a deploy someone just made from another terminal.
func (s *apiServer) refreshDeployState(w http.ResponseWriter, r *http.Request) {
	s.deployStateByPath(w, r, true)
}

func (s *apiServer) deployStateByPath(w http.ResponseWriter, r *http.Request, force bool) {
	s.byPath(w, r, func(e template.ScaffoldEntry, systems map[string]string) any {
		return s.cachedDeployState(r.Context(), s.summarize(e, systems), force)
	})
}

// byPath resolves the {path...} wildcard to a scaffolded integration and serves
// what render makes of it, or 404s. Shared so the detail and deployment
// endpoints agree about which identifiers name the same integration.
func (s *apiServer) byPath(w http.ResponseWriter, r *http.Request, render func(template.ScaffoldEntry, map[string]string) any) {
	want := strings.Trim(r.PathValue("path"), "/")
	// An integration at the workspace root has the identifier "." (see
	// relPath), but a browser strips the "." segment from /api/integrations/.
	// so it arrives empty — treat the two as the same request.
	if want == "" {
		want = "."
	}
	entries, systems := s.scan()
	for _, e := range entries {
		if s.relPath(e.Path) == want {
			writeJSON(w, http.StatusOK, render(e, systems))
			return
		}
	}
	writeError(w, http.StatusNotFound, "integration not found: "+want)
}

// cachedDeployState returns one integration's deployment state — computed on
// first use or when force is set, reused otherwise.
//
// The mutex is held across the provider call rather than only around the map,
// because the command takes an exclusive lock on the shared GitOps checkout:
// two requests running it at once would have one of them fail on the other's
// lock. Serialising here turns that failure into a wait, and concurrent requests
// for the same integration share one run — the same bargain cachedTopologies
// makes with topoMu.
func (s *apiServer) cachedDeployState(ctx context.Context, sum integrationSummary, force bool) deployState {
	s.depMu.Lock()
	defer s.depMu.Unlock()

	if state, ok := s.depStates[sum.Path]; ok && !force {
		return state
	}

	ctx, cancel := context.WithTimeout(ctx, deployStateTimeout)
	defer cancel()
	state := s.dep(ctx, sum)

	if s.depStates == nil {
		s.depStates = map[string]deployState{}
	}
	s.depStates[sum.Path] = state
	return state
}

// enrich reads optional on-disk context for an integration. The entry's Path
// is already usable from the process working directory (see ListScaffolds), so
// it doubles as the project directory.
func (s *apiServer) enrich(e template.ScaffoldEntry, systems map[string]string) integrationDetail {
	dir := e.Path
	return integrationDetail{
		integrationSummary: s.summarize(e, systems),
		Agents:             readDoc(dir, "AGENTS.md"),
		Readme:             readDoc(dir, "README.md"),
		Components:         readComponents(filepath.Join(dir, "local", "dapr-components")),
		PipelineSteps:      listNames(filepath.Join(dir, "src", "Process", "Steps")),
	}
}

// readDoc returns the named text file's (possibly truncated) contents, or nil
// if it is absent or unreadable.
func readDoc(dir, name string) *fileDoc {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	if len(data) > maxDocBytes {
		data = data[:maxDocBytes]
	}
	return &fileDoc{Name: name, Content: string(data)}
}

// listNames returns the sorted base names of the files directly under dir, or
// nil if dir is absent or unreadable.
func listNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, ent := range ents {
		if !ent.IsDir() {
			names = append(names, ent.Name())
		}
	}
	sort.Strings(names)
	return names
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
