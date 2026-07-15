package blueprint

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

const testTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-blueprint
  title: Test
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

// newBlueprintServer serves a tarball containing a single blueprint named
// "test-blueprint" laid out as the v1 model expects: <blueprint>/template.yaml
// plus <blueprint>/skeleton/<files>.
func newBlueprintServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", map[string]string{
		"test-blueprint/template.yaml":           testTemplateYAML,
		"test-blueprint/skeleton/README.md.tmpl": "{{ .integrationName }} in {{ .namespace }}\n",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

func TestCreateWritesOutputJSON(t *testing.T) {
	srv := newBlueprintServer(t, "v9.9.9")
	defer srv.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	jsonPath := filepath.Join(t.TempDir(), "result.json")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Blueprint:     "test-blueprint",
		OutputDir:     outDir,
		Version:       "v9.9.9",
		SetValues:     map[string]any{"integrationName": "orders"},
		NoInput:       true,
		OutputJSON:    jsonPath,
		Stderr:        &stderr,
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var got CreateResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, string(data))
	}
	if got.Template != "test-blueprint" {
		t.Errorf("Template = %q", got.Template)
	}
	if got.Owner != "o" || got.Repo != "r" {
		t.Errorf("Owner/Repo = %q/%q", got.Owner, got.Repo)
	}
	if got.Version != "v9.9.9" {
		t.Errorf("Version = %q", got.Version)
	}
	if !filepath.IsAbs(got.OutputDir) {
		t.Errorf("OutputDir should be absolute: %q", got.OutputDir)
	}
	if got.Values["integrationName"] != "orders" {
		t.Errorf("values[integrationName] = %v", got.Values["integrationName"])
	}
	if got.Values["namespace"] != "default" {
		t.Errorf("values[namespace] = %v (default should layer in)", got.Values["namespace"])
	}
}

func TestCreateWritesScaffoldFile(t *testing.T) {
	srv := newBlueprintServer(t, "v2.0.0")
	defer srv.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	err := Create(context.Background(), CreateOptions{
		Blueprint:     "test-blueprint",
		OutputDir:     outDir,
		Version:       "v2.0.0",
		SetValues:     map[string]any{"integrationName": "orders"},
		NoInput:       true,
		Stderr:        &bytes.Buffer{},
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := LoadScaffold(filepath.Join(outDir, filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("LoadScaffold: %v", err)
	}
	if got.SchemaVersion != ScaffoldSchemaVersion {
		t.Errorf("SchemaVersion = %d", got.SchemaVersion)
	}
	if got.Blueprint != "test-blueprint" || got.Owner != "o" || got.Repo != "r" || got.Version != "v2.0.0" {
		t.Errorf("scaffold identity = %q %q/%q@%q", got.Blueprint, got.Owner, got.Repo, got.Version)
	}
	if got.Values["integrationName"] != "orders" || got.Values["namespace"] != "default" {
		t.Errorf("Values = %v", got.Values)
	}
}

func TestCreateOutputJSONStdout(t *testing.T) {
	srv := newBlueprintServer(t, "v1")
	defer srv.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	var stdout, stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Blueprint:     "test-blueprint",
		OutputDir:     outDir,
		Version:       "v1",
		SetValues:     map[string]any{"integrationName": "x"},
		NoInput:       true,
		OutputJSON:    "-",
		Stdout:        &stdout,
		Stderr:        &stderr,
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(stdout.String(), `"template": "test-blueprint"`) {
		t.Errorf("stdout missing blueprint field: %s", stdout.String())
	}
	// Human-readable logs must stay on stderr so stdout is pure JSON.
	if strings.Contains(stdout.String(), "fetching") {
		t.Errorf("stdout should not contain log lines: %s", stdout.String())
	}
}

func TestCreateDoesNotCreateOutputDirWhenValidationFails(t *testing.T) {
	srv := newBlueprintServer(t, "v1")
	defer srv.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	err := Create(context.Background(), CreateOptions{
		Blueprint:     "test-blueprint",
		OutputDir:     outDir,
		Version:       "v1",
		NoInput:       true,
		Stderr:        &bytes.Buffer{},
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "missing required parameter") {
		t.Fatalf("expected missing required parameter error, got %v", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("failed create should not create output dir, stat err=%v", statErr)
	}
}

func TestCreateReadsStdinValues(t *testing.T) {
	srv := newBlueprintServer(t, "v1")
	defer srv.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	jsonPath := filepath.Join(t.TempDir(), "result.json")

	err := Create(context.Background(), CreateOptions{
		Blueprint:     "test-blueprint",
		OutputDir:     outDir,
		Version:       "v1",
		Files:         []string{StdinValuesPath},
		NoInput:       true,
		OutputJSON:    jsonPath,
		Stdin:         bytes.NewBufferString(`{"integrationName": "from-stdin", "namespace": "ns2"}`),
		Stderr:        &bytes.Buffer{},
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(readme) != "from-stdin in ns2\n" {
		t.Errorf("README = %q", string(readme))
	}

	data, _ := os.ReadFile(jsonPath)
	if !bytes.Contains(data, []byte(`"namespace": "ns2"`)) {
		t.Errorf("result JSON missing namespace value: %s", string(data))
	}
}
