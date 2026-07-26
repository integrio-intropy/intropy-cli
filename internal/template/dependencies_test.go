package template

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const depComponentYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: component
  labels:
    intropy.dev/block-kind: extractor
    intropy.dev/data-flow: "in"
spec:
  parameters:
    type: object
    required: [name]
    properties:
      name:
        type: string
      org:
        type: string
        default: Acme
  dependencies:
    - template: shared
      output: '{{ .org }}.Models'
      values:
        name: '{{ .org }}.Models'
`

const depSharedYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: shared
  labels:
    intropy.dev/template-role: shared-library
spec:
  parameters:
    type: object
    required: [name]
    properties:
      name:
        type: string
`

// newDependencyServer serves a tarball holding a component template that
// declares a dependency on the shared template in the same repo.
func newDependencyServer(t *testing.T, tag string, extra map[string]string) *httptest.Server {
	t.Helper()
	files := map[string]string{
		"component/template.yaml":           depComponentYAML,
		"component/skeleton/README.md.tmpl": "component {{ .name }}\n",
		"shared/template.yaml":              depSharedYAML,
		"shared/skeleton/lib.txt.tmpl":      "shared {{ .name }}\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	tarball := buildTarGz(t, "owner-repo-abc123", files)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

func createComponent(t *testing.T, srv *httptest.Server, outDir string, stderr *bytes.Buffer, jsonPath string, force bool) error {
	t.Helper()
	return Create(context.Background(), CreateOptions{
		Template:      "component",
		OutputDir:     outDir,
		Version:       "v1",
		SetValues:     map[string]any{"name": "orders"},
		Force:         force,
		NoInput:       true,
		OutputJSON:    jsonPath,
		Stdout:        &bytes.Buffer{},
		Stderr:        stderr,
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
}

func TestCreateRendersMissingDependency(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	outDir := filepath.Join(parent, "orders")
	jsonPath := filepath.Join(t.TempDir(), "result.json")
	var stderr bytes.Buffer

	if err := createComponent(t, srv, outDir, &stderr, jsonPath, false); err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	lib, err := os.ReadFile(filepath.Join(parent, "Acme.Models", "lib.txt"))
	if err != nil {
		t.Fatalf("dependency not rendered: %v", err)
	}
	if string(lib) != "shared Acme.Models\n" {
		t.Errorf("lib.txt = %q", string(lib))
	}

	depScaffold, err := LoadScaffold(filepath.Join(parent, "Acme.Models", filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("dependency scaffold: %v", err)
	}
	if depScaffold.Template != "shared" || depScaffold.Role != RoleSharedLibrary || depScaffold.Version != "v1" {
		t.Errorf("dependency scaffold = %+v", depScaffold)
	}
	if depScaffold.BlockKind != "" || depScaffold.DataFlow != "" {
		t.Errorf("shared library should have no block kind, got %q/%q", depScaffold.BlockKind, depScaffold.DataFlow)
	}

	compScaffold, err := LoadScaffold(filepath.Join(outDir, filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("component scaffold: %v", err)
	}
	if compScaffold.BlockKind != BlockKindExtractor || compScaffold.DataFlow != "in" {
		t.Errorf("component block kind = %q/%q, want %q/\"in\"", compScaffold.BlockKind, compScaffold.DataFlow, BlockKindExtractor)
	}
	want := DependencyRecord{Template: "shared", Dir: "../Acme.Models"}
	if len(compScaffold.DependsOn) != 1 || compScaffold.DependsOn[0] != want {
		t.Errorf("DependsOn = %+v", compScaffold.DependsOn)
	}

	var result CreateResult
	data, _ := os.ReadFile(jsonPath)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Dependencies) != 1 || result.Dependencies[0].Action != "created" || result.Dependencies[0].Template != "shared" {
		t.Errorf("result.Dependencies = %+v", result.Dependencies)
	}
}

func TestCreateSkipsExistingDependency(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	if err := createComponent(t, srv, filepath.Join(parent, "first"), &bytes.Buffer{}, "", false); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	libPath := filepath.Join(parent, "Acme.Models", "lib.txt")
	if err := os.WriteFile(libPath, []byte("edited by team\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonPath := filepath.Join(t.TempDir(), "result.json")
	var stderr bytes.Buffer
	if err := createComponent(t, srv, filepath.Join(parent, "second"), &stderr, jsonPath, false); err != nil {
		t.Fatalf("second Create: %v\nstderr: %s", err, stderr.String())
	}

	lib, _ := os.ReadFile(libPath)
	if string(lib) != "edited by team\n" {
		t.Errorf("existing dependency was overwritten: %q", string(lib))
	}
	if !strings.Contains(stderr.String(), "already scaffolded") {
		t.Errorf("stderr missing skip line: %s", stderr.String())
	}

	var result CreateResult
	data, _ := os.ReadFile(jsonPath)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Dependencies) != 1 || result.Dependencies[0].Action != "exists" {
		t.Errorf("result.Dependencies = %+v", result.Dependencies)
	}

	// The second component still records the dependency edge.
	compScaffold, err := LoadScaffold(filepath.Join(parent, "second", filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if len(compScaffold.DependsOn) != 1 {
		t.Errorf("DependsOn = %+v", compScaffold.DependsOn)
	}
}

func TestCreateForceDoesNotPropagateToDependency(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	outDir := filepath.Join(parent, "orders")
	if err := createComponent(t, srv, outDir, &bytes.Buffer{}, "", false); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	libPath := filepath.Join(parent, "Acme.Models", "lib.txt")
	if err := os.WriteFile(libPath, []byte("edited by team\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-render the component itself with --force; the sibling must survive.
	if err := createComponent(t, srv, outDir, &bytes.Buffer{}, "", true); err != nil {
		t.Fatalf("forced Create: %v", err)
	}
	lib, _ := os.ReadFile(libPath)
	if string(lib) != "edited by team\n" {
		t.Errorf("--force overwrote the dependency: %q", string(lib))
	}
}

func TestCreateFailsOnForeignScaffoldInDependencyDir(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	depDir := filepath.Join(parent, "Acme.Models")
	if err := WriteScaffold(depDir, Scaffold{SchemaVersion: 1, Template: "something-else"}); err != nil {
		t.Fatal(err)
	}

	err := createComponent(t, srv, filepath.Join(parent, "orders"), &bytes.Buffer{}, "", false)
	if err == nil || !strings.Contains(err.Error(), `scaffolded from template "something-else"`) {
		t.Fatalf("expected foreign-template error, got %v", err)
	}
}

func TestCreateFailsOnUnmanagedNonEmptyDependencyDir(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	depDir := filepath.Join(parent, "Acme.Models")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "notes.txt"), []byte("hands off"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := createComponent(t, srv, filepath.Join(parent, "orders"), &bytes.Buffer{}, "", false)
	if err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("expected unmanaged-directory error, got %v", err)
	}
}

func TestCreateWarnsOnDependencyValueDrift(t *testing.T) {
	srv := newDependencyServer(t, "v1", nil)
	defer srv.Close()

	parent := t.TempDir()
	if err := createComponent(t, srv, filepath.Join(parent, "first"), &bytes.Buffer{}, "", false); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Second component derives a different value for the shared project's
	// name parameter but targets the same directory.
	var stderr bytes.Buffer
	err := Create(context.Background(), CreateOptions{
		Template:      "component",
		OutputDir:     filepath.Join(parent, "second"),
		Version:       "v1",
		SetValues:     map[string]any{"name": "invoices", "org": "Acme"},
		NoInput:       true,
		Stderr:        &stderr,
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if strings.Contains(stderr.String(), "warning:") {
		t.Errorf("same derived values should not warn: %s", stderr.String())
	}

	// Corrupt the recorded value to simulate drift.
	depScaffoldPath := filepath.Join(parent, "Acme.Models", filepath.FromSlash(ScaffoldRelPath))
	s, err := LoadScaffold(depScaffoldPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Values["name"] = "Other.Models"
	if err := WriteScaffold(filepath.Join(parent, "Acme.Models"), *s); err != nil {
		t.Fatal(err)
	}

	stderr.Reset()
	err = createComponent(t, srv, filepath.Join(parent, "third"), &stderr, "", false)
	if err != nil {
		t.Fatalf("third Create: %v", err)
	}
	if !strings.Contains(stderr.String(), "warning: dependency Acme.Models") {
		t.Errorf("expected drift warning, got: %s", stderr.String())
	}
}

func TestCreateRejectsDependencyOutputEscapingParent(t *testing.T) {
	bad := strings.Replace(depComponentYAML, "output: '{{ .org }}.Models'", "output: '../{{ .org }}.Models'", 1)
	srv := newDependencyServer(t, "v1", map[string]string{"component/template.yaml": bad})
	defer srv.Close()

	err := createComponent(t, srv, filepath.Join(t.TempDir(), "orders"), &bytes.Buffer{}, "", false)
	if err == nil || !strings.Contains(err.Error(), "single path segment") {
		t.Fatalf("expected path-segment error, got %v", err)
	}
}

func TestCreateFailsWhenDependencyMissesRequiredParams(t *testing.T) {
	bad := strings.Replace(depComponentYAML, "        name: '{{ .org }}.Models'\n", "", 1)
	srv := newDependencyServer(t, "v1", map[string]string{"component/template.yaml": bad})
	defer srv.Close()

	err := createComponent(t, srv, filepath.Join(t.TempDir(), "orders"), &bytes.Buffer{}, "", false)
	if err == nil || !strings.Contains(err.Error(), "dependency shared") || !strings.Contains(err.Error(), "missing required parameter") {
		t.Fatalf("expected dependency missing-parameter error, got %v", err)
	}
}

func TestCreateRendersTransitiveDependencies(t *testing.T) {
	sharedWithDep := `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: shared
  labels:
    intropy.dev/template-role: shared-library
spec:
  parameters:
    type: object
    required: [name]
    properties:
      name:
        type: string
  dependencies:
    - template: nested
      output: 'Nested'
      values:
        name: 'Nested'
`
	nestedYAML := strings.Replace(depSharedYAML, "name: shared", "name: nested", 1)
	srv := newDependencyServer(t, "v1", map[string]string{
		"component/template.yaml":     strings.Replace(depComponentYAML, "org:", "org:", 1),
		"shared/template.yaml":        sharedWithDep,
		"nested/template.yaml":        nestedYAML,
		"nested/skeleton/nested.tmpl": "nested {{ .name }}\n",
	})
	defer srv.Close()

	parent := t.TempDir()
	jsonPath := filepath.Join(t.TempDir(), "result.json")
	if err := createComponent(t, srv, filepath.Join(parent, "orders"), &bytes.Buffer{}, jsonPath, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(filepath.Join(parent, "Nested", "nested")); err != nil {
		t.Fatalf("transitive dependency not rendered: %v", err)
	}

	var result CreateResult
	data, _ := os.ReadFile(jsonPath)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Dependencies) != 2 {
		t.Errorf("expected 2 dependency results, got %+v", result.Dependencies)
	}
}
