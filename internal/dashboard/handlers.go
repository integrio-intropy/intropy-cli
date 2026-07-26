package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// maxDocBytes caps how much of an enrichment document (AGENTS.md, README.md)
// the detail endpoint returns, so a huge file cannot bloat a response.
const maxDocBytes = 128 * 1024

// apiServer holds the request-independent state for the JSON API.
type apiServer struct {
	root    string
	version string
	topo    topologyProvider

	// topoMu guards the cached provider result. Fetching runs the hosts'
	// graph verbs (a dotnet build on first run), so the result is computed
	// once — on the first /api/topology request — and reused until an
	// explicit refresh.
	topoMu     sync.Mutex
	topoLoaded bool
	topoCache  topologyReport
}

// topologyReport is the /api/topology payload: every declared topology plus
// the per-host failures the UI renders. Topologies is always an array (never
// null) even when empty.
type topologyReport struct {
	Topologies []topology.Entry `json:"topologies"`
	Errors     []string         `json:"errors,omitempty"`
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

// newHandler wires the API routes and the SPA static handler onto a mux.
func newHandler(root, version string, topo topologyProvider) (http.Handler, error) {
	api := &apiServer{root: root, version: version, topo: topo}
	static, err := staticHandler()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", api.health)
	mux.HandleFunc("GET /api/integrations", api.listIntegrations)
	mux.HandleFunc("GET /api/integrations/{path...}", api.getIntegration)
	mux.HandleFunc("GET /api/flow", api.flow)
	mux.HandleFunc("GET /api/topology", api.topologies)
	mux.HandleFunc("POST /api/topology/refresh", api.refreshTopologies)
	mux.Handle("/", static)
	return mux, nil
}

func (s *apiServer) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
	})
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
	if name, ok := host.Values["name"].(string); ok && name != "" {
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
		if grand := filepath.Dir(parent); parent != root && grand != root {
			sum.Domain = filepath.Base(grand)
		}
		return sum
	}
	if path == root || parent == root {
		return sum
	}
	sum.System = filepath.Base(parent)
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

// topologyReport returns the cached provider result, computing it on first
// use or when force is set. Concurrent requests share one computation.
func (s *apiServer) topologyReport(ctx context.Context, force bool) topologyReport {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	if s.topoLoaded && !force {
		return s.topoCache
	}
	entries, errs := s.topo(ctx)
	report := topologyReport{Topologies: make([]topology.Entry, 0, len(entries)), Errors: errs}
	for _, e := range entries {
		e.Path = s.relPath(e.Path)
		report.Topologies = append(report.Topologies, e)
	}
	s.topoCache, s.topoLoaded = report, true
	return report
}

func (s *apiServer) getIntegration(w http.ResponseWriter, r *http.Request) {
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
			writeJSON(w, http.StatusOK, s.enrich(e, systems))
			return
		}
	}
	writeError(w, http.StatusNotFound, "integration not found: "+want)
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
