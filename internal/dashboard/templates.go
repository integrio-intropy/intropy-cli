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
	owner         string
	repo          string
	githubBaseURL string
}

// fetchLibrary resolves and downloads the provider's library release. The
// caller owns the returned Library and must Close it.
func (p templatesProvider) fetchLibrary(ctx context.Context) (*template.Library, error) {
	return template.FetchLibrary(ctx, template.LibraryOptions{
		Version:       p.version,
		UserAgent:     p.userAgent,
		Owner:         p.owner,
		Repo:          p.repo,
		GitHubBaseURL: p.githubBaseURL,
	})
}

// createRequest is the POST /api/templates/{name}/create body.
type createRequest struct {
	// Name picks the output directory under the workspace root — the CLI's
	// --out-dir. It is deliberately decoupled from values.name, the schema
	// parameter: a template can want a PascalCase project name next to a
	// kebab-case directory, and the CLI separates the two the same way
	// (--set name=X --out-dir y). Required.
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
// describes are local reads on the already-extracted tarball; one malformed
// manifest drops that entry rather than hiding the rest of the library.
func (s *apiServer) listTemplates(w http.ResponseWriter, r *http.Request) {
	lib, err := s.templates.fetchLibrary(r.Context())
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
// ordered field list the form renders from.
func (s *apiServer) getTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := template.ValidateTemplateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lib, err := s.templates.fetchLibrary(r.Context())
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
	writeJSON(w, http.StatusOK, result)
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

	outputDir, values, err := s.resolveCreate(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	prep, err := template.PrepareCreate(r.Context(), template.CreateOptions{
		Template:      name,
		Version:       s.templates.version,
		SetValues:     values,
		UserAgent:     s.templates.userAgent,
		Owner:         s.templates.owner,
		Repo:          s.templates.repo,
		GitHubBaseURL: s.templates.githubBaseURL,
	})
	if err != nil {
		writeError(w, createErrorStatus(err), err.Error())
		return
	}
	defer prep.Cleanup()

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

// resolveCreate folds the request into the output directory and value set
// `int create --name` would produce: name doubles as values.name and as the
// output directory under dir (the workspace root when dir is empty), confined
// there so the endpoint can never write outside the tree the dashboard serves.
func (s *apiServer) resolveCreate(req createRequest) (string, map[string]any, error) {
	if req.Name == "" {
		return "", nil, errors.New("name is required")
	}
	if strings.ContainsAny(req.Name, `/\`) || req.Name == "." || req.Name == ".." {
		return "", nil, fmt.Errorf("invalid name %q (must be a single path segment)", req.Name)
	}
	segments, err := splitCreateDir(req.Dir)
	if err != nil {
		return "", nil, err
	}

	values := req.Values
	if values == nil {
		values = map[string]any{}
	}
	// A template that does not declare `name` still gets the directory as
	// values.name — the CLI's --name sugar. When the template does declare
	// it, the form's value wins; the two are separate concerns.
	if _, ok := values["name"]; !ok {
		values["name"] = req.Name
	}
	for key := range values {
		if isReservedValueKey(key) {
			return "", nil, fmt.Errorf("value %q is reserved and cannot be supplied", key)
		}
	}

	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", nil, err
	}
	parentDir := filepath.Join(append([]string{s.root}, segments...)...)
	if len(segments) > 0 {
		// The dir must already exist: creating into a system means the system
		// directory is there; anything else is a client mistake, not a mkdir.
		if info, statErr := os.Stat(parentDir); statErr != nil || !info.IsDir() {
			return "", nil, fmt.Errorf("directory %q does not exist under the workspace root", req.Dir)
		}
	}
	outputDir := filepath.Join(parentDir, req.Name)
	outAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", nil, err
	}
	want := filepath.Join(append(append([]string{rootAbs}, segments...), req.Name)...)
	if outAbs != want {
		return "", nil, fmt.Errorf("invalid name %q (output would escape the workspace root)", req.Name)
	}
	return outputDir, values, nil
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
