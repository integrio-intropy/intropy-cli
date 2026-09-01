package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/huandu/xstrings"
	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// templatesProvider is the request-independent source for the template
// endpoints: the library release list, show and create all fetch and render
// against. Resolving it once per server keeps the release the form was filled
// against and the release the run renders from the same — a latest-release
// lookup per request could drift between the two.
type templatesProvider struct {
	version   string
	userAgent string

	// Test overrides; production leaves these zero-valued and gets the
	// official library.
	owner  string
	repo   string
	source template.SourceOptions
}

// fetchLibrary resolves and checks out the provider's library release. tag
// pins the release the server has already resolved, so only the first fetch
// of a process pays a latest-release lookup; an explicitly configured version
// still wins, because pinning is the caller's decision, not the cache's.
func (p templatesProvider) fetchLibrary(ctx context.Context, tag string) (*template.Library, error) {
	version := p.version
	if version == "" {
		version = tag
	}
	return template.FetchLibrary(ctx, template.LibraryOptions{
		Version:   version,
		UserAgent: p.userAgent,
		Owner:     p.owner,
		Repo:      p.repo,
		Source:    p.source,
	})
}

// fetchLibrary serves every template endpoint, resolving the library release
// once per process and reusing it after: the release a form is filled against
// and the release its run renders from can then never differ, and a form's
// suggestion refreshes cost no GitHub requests at all.
//
// The lock spans the fetch rather than just the tag read. A cold cache means
// a clone, and letting concurrent requests each start one would be the
// thundering herd the cache exists to prevent — the same bargain topoMu makes
// for the hosts' graph verbs. Once warm, every fetch is a local read.
func (s *apiServer) fetchLibrary(ctx context.Context) (*template.Library, error) {
	s.tagMu.Lock()
	defer s.tagMu.Unlock()
	lib, err := s.templates.fetchLibrary(ctx, s.tag)
	if err != nil {
		return nil, err
	}
	// Whatever release the fetch settled on is the one the process holds,
	// including a tag the offline fallback chose rather than the latest.
	s.tag = lib.Version
	return lib, nil
}

// libraryTag returns the release every template render pins to, resolving it
// on first use like fetchLibrary does. Handlers that hand a version to another
// package need the tag itself rather than a Library — without it those renders
// would resolve their own, which is the per-request lookup this cache exists
// to remove.
func (s *apiServer) libraryTag(ctx context.Context) (string, error) {
	lib, err := s.fetchLibrary(ctx)
	if err != nil {
		return "", err
	}
	defer lib.Close()
	return lib.Version, nil
}

// refreshTemplates drops the resolved release so the next fetch resolves the
// latest one again, then serves the fresh listing. It is how a template
// release cut while the dashboard runs is picked up without a restart —
// the same escape hatch POST /api/topology/refresh offers.
func (s *apiServer) refreshTemplates(w http.ResponseWriter, r *http.Request) {
	s.tagMu.Lock()
	s.tag = ""
	s.tagMu.Unlock()
	s.listTemplates(w, r)
}

// createRequest is the POST /api/templates/{name}/create body.
type createRequest struct {
	// Name folds into values.name when the caller did not set it directly
	// (the CLI's --name sugar) and defaults the output directory:
	// kebab-cased, under Dir. Empty means the resolved "name" parameter
	// decides the directory instead — the no-prompt default both surfaces
	// prefer.
	Name string `json:"name"`

	// Dir is the root-relative, slash-separated directory the output renders
	// under; "" or "." is the workspace root — the pre-Dir behavior. It must
	// already exist: the endpoint scaffolds into systems, it does not invent
	// directory trees.
	Dir string `json:"dir,omitempty"`

	// Values are the template parameters, as --set would supply them.
	Values map[string]any `json:"values"`

	// Force allows rendering into a non-empty output directory.
	Force bool `json:"force,omitempty"`
}

// createResponse is the 201 payload: CreateResult with the output directory
// made root-relative, matching the identifier space /api/integrations uses.
type createResponse struct {
	*template.CreateResult
	// Diagnostics is what the render wrote to stderr, one entry per line —
	// the fetch notice and each dependency render. Provenance, not failure.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// maxCreateBodyBytes caps a create request body. Parameter values are short
// scalars; a megabyte is already generous and keeps a malformed client from
// streaming unbounded input into memory.
const maxCreateBodyBytes = 1 << 20

// templateSummary is one list entry with the manifest metadata the create
// surfaces filter on — notably the intropy.dev/* labels a flow-view slot
// selects templates by. Additive beside the bare names `templates` keeps.
type templateSummary struct {
	Name        string            `json:"name"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// listTemplates mirrors `template list -o json` against the server's library
// release, adding per-template metadata (`entries`) on top of the names. The
// describes are local reads on the library checkout; one malformed manifest
// drops that entry rather than hiding the rest of the library.
func (s *apiServer) listTemplates(w http.ResponseWriter, r *http.Request) {
	lib, err := s.fetchLibrary(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer lib.Close()

	names, err := lib.List()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	entries := make([]templateSummary, 0, len(names))
	for _, name := range names {
		desc, err := lib.Describe(name)
		if err != nil {
			continue
		}
		entries = append(entries, templateSummary{
			Name:        name,
			Title:       desc.Title,
			Description: desc.Description,
			Labels:      desc.Labels,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner":     lib.Owner,
		"repo":      lib.Repo,
		"version":   lib.Version,
		"templates": names,
		"entries":   entries,
	})
}

// getTemplate mirrors `template show -o json` for one template, adding the
// ordered field list the form renders from. The optional ?dir query names a
// root-relative directory the way createRequest.Dir does; when present, each
// field gains the parameter suggestions the workspace under it implies —
// the same candidates `int create` would prompt with there. Without it the
// response is context-free and suggestion-free.
func (s *apiServer) getTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := template.ValidateTemplateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lib, err := s.fetchLibrary(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer lib.Close()

	result, err := lib.Describe(name)
	if err != nil {
		writeError(w, templateFetchStatus(err), err.Error())
		return
	}

	if dir := r.URL.Query().Get("dir"); dir != "" {
		confirmed, err := parseConfirmedQuery(r.URL.Query())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.suggestForDir(result, dir, confirmed); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// getTemplateSuggestions serves only the suggestion lists of a dir-scoped
// detail: the refresh a form issues when an answered parameter changes what
// the workspace implies for the rest (a picked topic narrows the contract
// candidates). It is the same computation getTemplate runs, without the
// library fetch — a mid-form refresh must never swap the manifest the form
// was filled against.
func (s *apiServer) getTemplateSuggestions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := template.ValidateTemplateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lib, err := s.fetchLibrary(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer lib.Close()

	result, err := lib.Describe(name)
	if err != nil {
		writeError(w, templateFetchStatus(err), err.Error())
		return
	}

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeError(w, http.StatusBadRequest, "dir is required (suggestions derive from the workspace under it)")
		return
	}
	confirmed, err := parseConfirmedQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.suggestForDir(result, dir, confirmed); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	suggestions := make(map[string][]string, len(result.Fields))
	for _, f := range result.Fields {
		suggestions[f.Name] = f.Suggestions
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

// suggestForDir populates each field's suggestions from the workspace under
// dir, chained off the values the form has already confirmed — the same
// inputs `int create` prompts with (workspace facts plus confirmed values).
func (s *apiServer) suggestForDir(result *template.DescribeResult, dir string, confirmed map[string]any) error {
	facts, err := s.factsForDir(dir)
	if err != nil {
		return err
	}
	suggestions := template.Suggest(result.Fields, facts, confirmed)
	for i := range result.Fields {
		result.Fields[i].Suggestions = suggestions[result.Fields[i].Name]
	}
	return nil
}

// parseConfirmedQuery decodes the repeated `set` query parameter
// (?set=topic=orders&set=pubsub=events) into the confirmed-values map
// Suggest chains from. Values stay strings — prompt values do, too. A
// malformed pair is a 400: a silently dropped set would suggest candidates
// for the wrong workspace state.
func parseConfirmedQuery(q map[string][]string) (map[string]any, error) {
	pairs := q["set"]
	if len(pairs) == 0 {
		return nil, nil
	}
	confirmed := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid set %q (expected name=value)", pair)
		}
		confirmed[name] = value
	}
	return confirmed, nil
}

// factsForDir validates a root-relative dir with the create endpoint's
// rules and indexes the scaffold records under it. The dir must exist —
// suggesting from a system means the system directory is there.
func (s *apiServer) factsForDir(dir string) (*template.WorkspaceFacts, error) {
	segments, err := splitCreateDir(dir)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(append([]string{s.root}, segments...)...)
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("directory %q does not exist under the workspace root", dir)
	}
	facts, _ := system.LoadWorkspaceFacts(abs)
	facts.SetOrganization(s.organization)
	return facts, nil
}

// createTemplate runs `int create` for one template: the form's values
// resolve non-interactively (a missing required parameter is a 422 naming
// it), the skeleton renders under the workspace root, and the result comes
// back as served. Runs are serialized: two concurrent renders of the same
// name would race on the output directory.
func (s *apiServer) createTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := template.ValidateTemplateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req createRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCreateBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	values, err := createValues(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	// The render pins the release the form was filled against, which is the
	// whole point of holding one: a create that resolved its own could render
	// from a release the user never saw.
	tag, err := s.libraryTag(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	prep, err := template.PrepareCreate(r.Context(), template.CreateOptions{
		Template:  name,
		Version:   tag,
		SetValues: values,
		UserAgent: s.templates.userAgent,
		Owner:     s.templates.owner,
		Repo:      s.templates.repo,
		Source:    s.templates.source,
	})
	if err != nil {
		writeError(w, createErrorStatus(err), err.Error())
		return
	}
	defer prep.Cleanup()

	outputDir, err := s.resolveCreateDir(req, prep)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var logs bytes.Buffer
	result, err := template.RunCreate(prep, outputDir, req.Force, &logs)
	if err != nil {
		writeError(w, createErrorStatus(err), err.Error())
		return
	}
	result.OutputDir = s.relPath(result.OutputDir)
	writeJSON(w, http.StatusCreated, createResponse{
		CreateResult: result,
		Diagnostics:  diagnosticLines(logs.String()),
	})
}

// createValues folds the request's name into the value set `int create
// --name` would produce: name doubles as values.name when the caller did
// not set it directly. When the template does not declare `name`, the
// value still lands there — the CLI's sugar does the same.
func createValues(req createRequest) (map[string]any, error) {
	values := req.Values
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["name"]; !ok && req.Name != "" {
		values["name"] = req.Name
	}
	for key := range values {
		if isReservedValueKey(key) {
			return nil, fmt.Errorf("value %q is reserved and cannot be supplied", key)
		}
	}
	return values, nil
}

// resolveCreateDir picks the output directory, confined to the tree the
// dashboard serves so the endpoint can never write outside it. An explicit
// name kebab-cases under Dir (the workspace root when empty), as
// `int create --name` does; without a name the resolved "name" parameter
// kebab-cases the same way, and a template without one is the error
// --out-dir would have spared.
func (s *apiServer) resolveCreateDir(req createRequest, prep *template.PreparedCreate) (string, error) {
	segments, err := splitCreateDir(req.Dir)
	if err != nil {
		return "", err
	}
	parentDir := filepath.Join(append([]string{s.root}, segments...)...)
	if len(segments) > 0 {
		// The dir must already exist: creating into a system means the system
		// directory is there; anything else is a client mistake, not a mkdir.
		if info, statErr := os.Stat(parentDir); statErr != nil || !info.IsDir() {
			return "", fmt.Errorf("directory %q does not exist under the workspace root", req.Dir)
		}
	}

	name := req.Name
	if name == "" {
		v, ok := prep.Values["name"].(string)
		if !ok || v == "" {
			return "", errors.New("name is required (template declares no 'name' parameter to derive the directory from)")
		}
		name = v
	}
	leaf := xstrings.ToKebabCase(name)
	if strings.ContainsAny(leaf, `/\`) || leaf == "." || leaf == ".." {
		return "", fmt.Errorf("invalid output directory %q (must be a single path segment)", leaf)
	}

	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	outputDir := filepath.Join(parentDir, leaf)
	outAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	want := filepath.Join(append(append([]string{rootAbs}, segments...), leaf)...)
	if outAbs != want {
		return "", fmt.Errorf("invalid output directory %q (output would escape the workspace root)", leaf)
	}
	return outputDir, nil
}

// splitCreateDir validates a create request's dir and returns its segments.
// "" and "." mean the workspace root (no segments). Each segment must be a
// plain directory name — no separators besides "/", no dot entries, nothing
// hidden — so the joined path can only descend from the root.
func splitCreateDir(dir string) ([]string, error) {
	if dir == "" || dir == "." {
		return nil, nil
	}
	if strings.Contains(dir, `\`) {
		return nil, fmt.Errorf("invalid dir %q (must be slash-separated)", dir)
	}
	segments := strings.Split(dir, "/")
	for _, seg := range segments {
		if seg == "" || seg == ".." || strings.HasPrefix(seg, ".") {
			return nil, fmt.Errorf("invalid dir %q (each segment must be a plain directory name)", dir)
		}
	}
	return segments, nil
}

// isReservedValueKey reports whether key is one of the reserved value keys a
// caller injects after resolution (topology, component, …). Users never
// supply them — a form value under one would collide with InjectReserved's
// refusal, so the endpoint refuses first with a message that says why.
func isReservedValueKey(key string) bool {
	switch key {
	case template.ReservedTopologyKey,
		template.ReservedComponentKey,
		template.ReservedGitopsKey,
		template.ReservedScaffoldKey,
		template.ReservedEnvKey:
		return true
	}
	return false
}

// templateFetchStatus maps a library fetch failure to the status the client
// can act on: an unknown template is a 404, anything upstream is a 502.
func templateFetchStatus(err error) int {
	if strings.Contains(err.Error(), "not found in") {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

// createErrorStatus maps a create failure to the status the form renders:
// unmet parameters and name conflicts are 422 (the form highlights fields),
// a non-empty output directory is 409 (the form offers force), and a fetch
// failure is 502. Everything else is a plain 500.
func createErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not empty"):
		return http.StatusConflict
	case strings.Contains(msg, "missing required parameter"),
		strings.Contains(msg, "unknown parameter"),
		strings.Contains(msg, "not found in"):
		return http.StatusUnprocessableEntity
	case strings.Contains(msg, "fetch"), strings.Contains(msg, "download"):
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}
