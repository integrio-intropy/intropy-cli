package dashboard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// testTemplateYAML declares two parameters in a known order so the detail
// endpoint's `fields` can be asserted against declaration order.
const testTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-template
  title: Test
  labels:
    intropy.dev/block-kind: extractor
    intropy.dev/data-flow: in
spec:
  parameters:
    type: object
    required: [integrationName]
    properties:
      integrationName:
        type: string
      namespace:
        type: string
        default: default
`

// newTemplateLibraryServer serves the standard test template.
func newTemplateLibraryServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	return newTemplateLibraryServerWith(t, tag, testTemplateYAML)
}

// newTemplateLibraryServerWith fakes the GitHub endpoints the template
// provider calls: the latest-release lookup and the tarball holding one
// template with the given manifest.
func newTemplateLibraryServerWith(t *testing.T, tag, manifest string) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", map[string]string{
		"test-template/template.yaml":           manifest,
		"test-template/skeleton/README.md.tmpl": "{{ .integrationName }} in {{ .namespace }}\n",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

// buildTarGz packs entries (name → body) under a leading directory, the
// layout GitHub tarball responses and ExtractTarGz both expect.
func buildTarGz(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: prefix + "/" + name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// templateProviders returns providers wired at the fake library, with the
// topology and deploy providers stubbed out — a test about templates should
// not have to care what a host declares or GitOps pins.
func templateProviders(baseURL string) providers {
	return providers{
		topology: emptyTopo,
		deploy:   emptyDeploy,
		templates: templatesProvider{
			userAgent:     "test",
			owner:         "o",
			repo:          "r",
			githubBaseURL: baseURL,
		},
	}
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func TestListTemplates(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1.2.3")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := get(t, h, "/api/templates")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Owner     string   `json:"owner"`
		Repo      string   `json:"repo"`
		Version   string   `json:"version"`
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1.2.3" || got.Owner != "o" || got.Repo != "r" {
		t.Errorf("library ref = %s/%s@%s", got.Owner, got.Repo, got.Version)
	}
	if len(got.Templates) != 1 || got.Templates[0] != "test-template" {
		t.Errorf("templates = %v", got.Templates)
	}
}

func TestGetTemplateServesOrderedFields(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := get(t, h, "/api/templates/test-template")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	// The response is the bare `template show -o json` document — fields ride
	// on DescribeResult itself, so the endpoint has no payload of its own.
	var got struct {
		Template string `json:"template"`
		Fields   []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
			Default  any    `json:"default"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Template != "test-template" {
		t.Errorf("template = %q", got.Template)
	}
	// The point of `fields`: declaration order survives where the parameters
	// map loses it. integrationName is declared before namespace.
	if len(got.Fields) != 2 || got.Fields[0].Name != "integrationName" || got.Fields[1].Name != "namespace" {
		t.Fatalf("fields = %+v", got.Fields)
	}
	if !got.Fields[0].Required {
		t.Error("integrationName should be required")
	}
	if got.Fields[1].Default != "default" {
		t.Errorf("namespace default = %v", got.Fields[1].Default)
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := get(t, h, "/api/templates/no-such-template")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestGetTemplateRejectsPathSegments(t *testing.T) {
	h := testHandlerWith(t, t.TempDir(), providers{topology: emptyTopo, deploy: emptyDeploy})

	// Encoded so the mux does not resolve the segments before the handler
	// sees the name.
	rec := get(t, h, "/api/templates/..%2Fsecret")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestCreateTemplate(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"orders-erp","values":{"integrationName":"Orders ERP"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string         `json:"outputDir"`
		Version   string         `json:"version"`
		Values    map[string]any `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "orders-erp" {
		t.Errorf("outputDir = %q, want root-relative", got.OutputDir)
	}
	if got.Values["name"] != "orders-erp" {
		t.Errorf("values.name = %v", got.Values["name"])
	}

	// The render actually landed under the workspace root, values applied.
	data, err := os.ReadFile(filepath.Join(root, "orders-erp", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Orders ERP in default") {
		t.Errorf("rendered README = %q", data)
	}
	// And the scaffold record makes it visible to the catalog.
	entries, _ := scanRoot(root)
	if len(entries) != 1 || entries[0].Template != "test-template" {
		t.Errorf("scaffolds = %+v", entries)
	}
}

func TestCreateTemplatePascalNameKebabDir(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	// The CLI's --name default: a PascalCase name scaffolds the kebab-cased
	// directory while values.name keeps the verbatim spelling.
	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"OrderSync","values":{"integrationName":"Order Sync"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string         `json:"outputDir"`
		Values    map[string]any `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "order-sync" {
		t.Errorf("outputDir = %q, want order-sync", got.OutputDir)
	}
	if got.Values["name"] != "OrderSync" {
		t.Errorf("values.name = %v, want the verbatim name", got.Values["name"])
	}
}

func TestCreateTemplateDerivesDirFromNameValue(t *testing.T) {
	// The form sends no name; the resolved "name" parameter kebab-cases
	// into the directory, the same convention the CLI's --name defaults by.
	srv := newTemplateLibraryServerWith(t, "v1", `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-template
spec:
  parameters:
    type: object
    required: [name, integrationName]
    properties:
      name:
        type: string
      integrationName:
        type: string
      namespace:
        type: string
        default: default
`)
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"values":{"name":"OrderSync","integrationName":"Order Sync"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string `json:"outputDir"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "order-sync" {
		t.Errorf("outputDir = %q, want order-sync", got.OutputDir)
	}
	if _, err := os.Stat(filepath.Join(root, "order-sync", "README.md")); err != nil {
		t.Fatalf("rendered file: %v", err)
	}
}

func TestCreateTemplateNamelessWithoutNameParameter(t *testing.T) {
	// The test template's parameters have no "name", so a nameless request
	// has nothing to derive a directory from.
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"values":{"integrationName":"A"}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Errorf("error should name the missing name: %s", rec.Body)
	}
}

func TestCreateTemplateMissingRequired(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create", `{"name":"x","values":{}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "integrationName") {
		t.Errorf("error should name the missing parameter: %s", rec.Body)
	}
}

func TestCreateTemplateDirNotEmpty(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "taken", "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"taken","values":{"integrationName":"A"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}

	// The force retry the UI offers on a 409.
	rec = postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"taken","values":{"integrationName":"A"},"force":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("force: status = %d, want 201: %s", rec.Code, rec.Body)
	}
}

func TestCreateTemplateDecouplesNameValue(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	// The CLI's --set name=X --out-dir y split: the schema's name parameter
	// and the output directory are separate concerns and may differ.
	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"out-dir","values":{"integrationName":"A","name":"Pascal.Name"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string         `json:"outputDir"`
		Values    map[string]any `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "out-dir" {
		t.Errorf("outputDir = %q", got.OutputDir)
	}
	if got.Values["name"] != "Pascal.Name" {
		t.Errorf("values.name = %v, want the form's value to win", got.Values["name"])
	}
}

func TestCreateTemplateRejectsTraversal(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	// JSON is decoded after URL decoding, so the handler sees the literal
	// "../escape" — exactly what a crafted client would send.
	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"../escape","values":{"integrationName":"A"}}`)
	if rec.Code == http.StatusCreated {
		t.Fatal("traversal name must not render")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
		t.Error("render escaped the workspace root")
	}
}

func TestCreateTemplateRejectsReservedValue(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"x","values":{"integrationName":"A","topology":{}}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "reserved") {
		t.Errorf("error should say why: %s", rec.Body)
	}
}

func TestListTemplatesIncludesLabels(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	h := testHandlerWith(t, t.TempDir(), templateProviders(srv.URL))

	rec := get(t, h, "/api/templates")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Entries []templateSummary `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "test-template" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	if got.Entries[0].Title != "Test" {
		t.Errorf("title = %q", got.Entries[0].Title)
	}
	// The labels are what a flow-view slot filters the palette by.
	if got.Entries[0].Labels["intropy.dev/block-kind"] != "extractor" {
		t.Errorf("labels = %v", got.Entries[0].Labels)
	}
}

func TestCreateTemplateIntoDir(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme", "erp"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"orders","dir":"acme/erp","values":{"integrationName":"Orders"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string `json:"outputDir"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The root-relative identifier the flow view joins ghost nodes on.
	if got.OutputDir != "acme/erp/orders" {
		t.Errorf("outputDir = %q, want %q", got.OutputDir, "acme/erp/orders")
	}
	if _, err := os.Stat(filepath.Join(root, "acme", "erp", "orders", "README.md")); err != nil {
		t.Fatalf("rendered file: %v", err)
	}
	entries, _ := scanRoot(root)
	if len(entries) != 1 || !strings.HasSuffix(filepath.ToSlash(entries[0].Path), "acme/erp/orders") {
		t.Errorf("scaffolds = %+v", entries)
	}
}

func TestCreateTemplateDirValidation(t *testing.T) {
	srv := newTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sys"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	cases := []struct{ name, dir string }{
		{"parent traversal", "../x"},
		{"inner traversal", "sys/../sys"},
		{"rooted", "/etc"},
		{"backslash", `sys\x`},
		{"empty segment", "sys//x"},
		{"trailing slash", "sys/"},
		{"hidden segment", ".hidden/x"},
		{"missing dir", "missing-dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"name":"x","dir":` + strconv.Quote(tc.dir) + `,"values":{"integrationName":"A"}}`
			rec := postJSON(t, h, "/api/templates/test-template/create", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("dir %q: status = %d, want 422: %s", tc.dir, rec.Code, rec.Body)
			}
		})
	}
	// None of the rejected dirs left anything beside the workspace root.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x")); !os.IsNotExist(err) {
		t.Error("render escaped the workspace root")
	}

	// "." is the workspace root — byte-for-byte the no-dir behavior.
	rec := postJSON(t, h, "/api/templates/test-template/create",
		`{"name":"at-root","dir":".","values":{"integrationName":"A"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("dir \".\": status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var got struct {
		OutputDir string `json:"outputDir"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "at-root" {
		t.Errorf("outputDir = %q, want %q", got.OutputDir, "at-root")
	}
}

// scanRoot is the test-facing half of apiServer.scan: the scaffold entries
// under root, without the system bookkeeping the API adds on top.
func scanRoot(root string) ([]scaffoldEntry, []string) {
	entries, systems := (&apiServer{root: root}).scan()
	out := make([]scaffoldEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, scaffoldEntry{Template: e.Template, Path: e.Path})
	}
	names := make([]string, 0, len(systems))
	for _, n := range systems {
		names = append(names, n)
	}
	return out, names
}

type scaffoldEntry struct {
	Template string
	Path     string
}

// topicTemplateYAML declares the wiring parameters a topic block prompts
// for, so the dir-scoped detail response can be asserted against workspace
// suggestions.
const topicTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: topic-template
spec:
  parameters:
    type: object
    required: [topic, contract]
    properties:
      topic:
        type: string
      contract:
        type: string
      pubsub:
        type: string
        default: pubsub
      organization:
        type: string
`

func newTopicTemplateLibraryServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", map[string]string{
		"topic-template/template.yaml":           topicTemplateYAML,
		"topic-template/skeleton/README.md.tmpl": "{{ .topic }}",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

func writeScaffoldRecord(t *testing.T, dir string, record string) {
	t.Helper()
	intropyDir := filepath.Join(dir, ".intropy")
	if err := os.MkdirAll(intropyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(intropyDir, "scaffold.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
}

const extractorScaffoldRecord = `{
  "schemaVersion": 1,
  "template": "extractor",
  "owner": "o",
  "repo": "r",
  "version": "v1",
  "values": {"appId": "order-extractor", "topic": "orders", "contract": "Order"},
  "blockKind": "extractor",
  "dataFlow": "in"
}`

func TestGetTemplateWithDirServesWorkspaceSuggestions(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), extractorScaffoldRecord)
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := get(t, h, "/api/templates/topic-template?dir=acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Fields []struct {
			Name        string   `json:"name"`
			Suggestions []string `json:"suggestions"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	suggestions := map[string][]string{}
	for _, f := range got.Fields {
		suggestions[f.Name] = f.Suggestions
	}
	if len(suggestions["topic"]) != 1 || suggestions["topic"][0] != "orders" {
		t.Errorf("topic suggestions = %v", suggestions["topic"])
	}
	if len(suggestions["contract"]) != 1 || suggestions["contract"][0] != "Order" {
		t.Errorf("contract suggestions = %v", suggestions["contract"])
	}
	// pubsub's only candidate equals its schema default, so it is dropped
	// rather than competing with the default bracket.
	if len(suggestions["pubsub"]) != 0 {
		t.Errorf("pubsub suggestions = %v", suggestions["pubsub"])
	}
}

func TestGetTemplateServesSeededOrganization(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), extractorScaffoldRecord)
	h := testHandlerWithOrg(t, root, templateProviders(srv.URL), "integrio")

	rec := get(t, h, "/api/templates/topic-template?dir=acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Fields []struct {
			Name        string   `json:"name"`
			Suggestions []string `json:"suggestions"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range got.Fields {
		if f.Name == "organization" {
			if len(f.Suggestions) != 1 || f.Suggestions[0] != "integrio" {
				t.Fatalf("organization suggestions = %v", f.Suggestions)
			}
			return
		}
	}
	t.Fatal("no organization field in response")
}

func TestGetTemplateWorkspaceOrganizationBeatsSeeded(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	record := strings.Replace(extractorScaffoldRecord,
		`"contract": "Order"`, `"contract": "Order", "organization": "acme"`, 1)
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), record)
	h := testHandlerWithOrg(t, root, templateProviders(srv.URL), "integrio")

	rec := get(t, h, "/api/templates/topic-template?dir=acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Fields []struct {
			Name        string   `json:"name"`
			Suggestions []string `json:"suggestions"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range got.Fields {
		if f.Name == "organization" {
			if len(f.Suggestions) != 1 || f.Suggestions[0] != "acme" {
				t.Fatalf("organization suggestions = %v", f.Suggestions)
			}
			return
		}
	}
	t.Fatal("no organization field in response")
}

func testHandlerWithOrg(t *testing.T, root string, p providers, org string) http.Handler {
	t.Helper()
	h, api, err := newHandler(root, "test", p)
	if err != nil {
		t.Fatal(err)
	}
	api.organization = org
	return h
}

func TestGetTemplateWithDirConflictingContractsSuggestNoContract(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), extractorScaffoldRecord)
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-loader"), `{
  "schemaVersion": 1,
  "template": "loader",
  "owner": "o",
  "repo": "r",
  "version": "v1",
  "values": {"appId": "order-loader", "topic": "orders", "contract": "OrderV2"},
  "blockKind": "loader",
  "dataFlow": "out"
}`)
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	rec := get(t, h, "/api/templates/topic-template?dir=acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got struct {
		Fields []struct {
			Name        string   `json:"name"`
			Suggestions []string `json:"suggestions"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range got.Fields {
		if f.Name == "contract" && len(f.Suggestions) != 0 {
			t.Errorf("conflicted contract should suggest nothing, got %v", f.Suggestions)
		}
	}
}

func TestGetTemplateDirValidation(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()
	root := t.TempDir()
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	for _, dir := range []string{"../outside", "no/such/dir"} {
		rec := get(t, h, "/api/templates/topic-template?dir="+dir)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("dir=%q: status = %d, want 400: %s", dir, rec.Code, rec.Body)
		}
	}
}

// loaderScaffoldRecord declares a second topic so the confirmed-topic
// chaining has something to narrow: with two topics known, an unchained
// contract query suggests both contracts while set=topic=orders narrows to
// the one the topic carries.
const loaderScaffoldRecord = `{
  "schemaVersion": 1,
  "template": "loader",
  "owner": "o",
  "repo": "r",
  "version": "v1",
  "values": {"appId": "audit-loader", "topic": "audits", "contract": "Audit"},
  "blockKind": "loader",
  "dataFlow": "out"
}`

func TestGetTemplateWithConfirmedValuesChainsSuggestions(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), extractorScaffoldRecord)
	writeScaffoldRecord(t, filepath.Join(systemDir, "audit-loader"), loaderScaffoldRecord)
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	suggestionsFor := func(url string) map[string][]string {
		t.Helper()
		rec := get(t, h, url)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", url, rec.Code, rec.Body)
		}
		var got struct {
			Fields []struct {
				Name        string   `json:"name"`
				Suggestions []string `json:"suggestions"`
			} `json:"fields"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, f := range got.Fields {
			out[f.Name] = f.Suggestions
		}
		return out
	}

	// Unconfirmed: every known topic and contract is a candidate.
	unchained := suggestionsFor("/api/templates/topic-template?dir=acme")
	if len(unchained["contract"]) != 2 {
		t.Errorf("contract suggestions without a confirmed topic = %v, want both known contracts", unchained["contract"])
	}

	// A confirmed topic narrows contract to the one that topic carries —
	// the chaining the form's refresh round-trips for.
	chained := suggestionsFor("/api/templates/topic-template?dir=acme&set=topic=orders")
	if len(chained["contract"]) != 1 || chained["contract"][0] != "Order" {
		t.Errorf("contract suggestions with set=topic=orders = %v, want [Order]", chained["contract"])
	}

	rec := get(t, h, "/api/templates/topic-template?dir=acme&set=not-a-pair")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed set: status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestGetTemplateSuggestions(t *testing.T) {
	srv := newTopicTemplateLibraryServer(t, "v1")
	defer srv.Close()

	root := t.TempDir()
	systemDir := filepath.Join(root, "acme")
	writeScaffoldRecord(t, filepath.Join(systemDir, "order-extractor"), extractorScaffoldRecord)
	writeScaffoldRecord(t, filepath.Join(systemDir, "audit-loader"), loaderScaffoldRecord)
	h := testHandlerWith(t, root, templateProviders(srv.URL))

	suggestionsFor := func(url string) map[string][]string {
		t.Helper()
		rec := get(t, h, url)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", url, rec.Code, rec.Body)
		}
		var got struct {
			Suggestions map[string][]string `json:"suggestions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got.Suggestions
	}

	all := suggestionsFor("/api/templates/suggestions/topic-template?dir=acme")
	if len(all["topic"]) != 2 || len(all["contract"]) != 2 {
		t.Errorf("suggestions = topic %v, contract %v; want both topics and contracts", all["topic"], all["contract"])
	}

	chained := suggestionsFor("/api/templates/suggestions/topic-template?dir=acme&set=topic=audits")
	if len(chained["contract"]) != 1 || chained["contract"][0] != "Audit" {
		t.Errorf("contract suggestions with set=topic=audits = %v, want [Audit]", chained["contract"])
	}
	// The confirmed field itself keeps its candidates — the form still
	// offers the other topics on the answered field.
	if len(chained["topic"]) != 2 {
		t.Errorf("topic suggestions = %v, want both topics still offered", chained["topic"])
	}

	for _, url := range []string{
		"/api/templates/suggestions/topic-template",
		"/api/templates/suggestions/topic-template?dir=../outside",
		"/api/templates/suggestions/topic-template?dir=acme&set=oops",
	} {
		rec := get(t, h, url)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", url, rec.Code, rec.Body)
		}
	}

	rec := get(t, h, "/api/templates/suggestions/no-such-template?dir=acme")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown template: status = %d, want 404: %s", rec.Code, rec.Body)
	}
}
