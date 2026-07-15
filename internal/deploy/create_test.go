package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/blueprint"
	"gopkg.in/yaml.v3"
)

const testManifestTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-blueprint-manifests
spec:
  parameters:
    type: object
    required: [name, imageRepository, appPort]
    properties:
      name:
        type: string
      imageRepository:
        type: string
      imageTag:
        type: string
        default: latest
      appPort:
        type: integer
        default: 5001
  values:
    appId: "{{ .name | lower }}"
`

const testDeploymentTmpl = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .appId }}
  labels:
    app: {{ .appId }}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: {{ .appId }}
  template:
    metadata:
      labels:
        app: {{ .appId }}
      annotations:
        dapr.io/enabled: "true"
        dapr.io/app-id: "{{ .appId }}"
        dapr.io/app-port: "{{ .appPort }}"
    spec:
      containers:
        - name: app
          image: {{ .imageRepository }}:{{ .imageTag }}
          ports:
            - containerPort: {{ .appPort }}
`

const testProdPubsubTmpl = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub
spec:
  type: pubsub.rabbitmq
  version: v1
  metadata:
    - name: connectionString
      secretKeyRef:
        name: "{{ .appId }}-pubsub"
        key: connectionString
    - name: customer
      value: "{{ .scaffold.customerName }}"
`

func testManifestFiles() map[string]string {
	return map[string]string{
		"test-blueprint/template.yaml":                                "unused-by-deploy",
		"test-blueprint/skeleton/README.md":                           "unused-by-deploy",
		"test-blueprint/manifests/template.yaml":                      testManifestTemplateYAML,
		"test-blueprint/manifests/skeleton/base/deployment.yaml.tmpl": testDeploymentTmpl,
		"test-blueprint/manifests/skeleton/base/service.yaml.tmpl": `apiVersion: v1
kind: Service
metadata:
  name: {{ .appId }}
spec:
  selector:
    app: {{ .appId }}
  ports:
    - port: 80
      targetPort: {{ .appPort }}
`,
		"test-blueprint/manifests/skeleton/base/kustomization.yaml": `resources:
  - deployment.yaml
  - service.yaml
`,
		"test-blueprint/manifests/skeleton/overlays/dev/kustomization.yaml": `resources:
  - ../../base
  - pubsub.yaml
`,
		"test-blueprint/manifests/skeleton/overlays/dev/pubsub.yaml": `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub
spec:
  type: pubsub.in-memory
  version: v1
`,
		"test-blueprint/manifests/skeleton/overlays/prod/kustomization.yaml.tmpl": `resources:
  - ../../base
  - pubsub.yaml
replicas:
  - name: {{ .appId }}
    count: 2
images:
  - name: {{ .imageRepository }}
    newTag: {{ .imageTag }}
`,
		"test-blueprint/manifests/skeleton/overlays/prod/pubsub.yaml.tmpl": testProdPubsubTmpl,
	}
}

func buildTarGz(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		full := name
		if prefix != "" {
			full = prefix + "/" + name
		}
		h := &tar.Header{Name: full, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
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

// newManifestsServer serves the fixture blueprint tarball for exactly one
// tag. Any hit on releases/latest fails the test: manifests create must use
// the version pinned in scaffold.json, never resolve "latest".
func newManifestsServer(t *testing.T, tag string, files map[string]string) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", files)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		t.Error("manifests create must not resolve releases/latest; it must use the pinned scaffold version")
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

// writeTestScaffold creates a project dir containing a scaffold record as
// int create would leave it (values round-tripped through JSON, so numbers
// arrive as float64).
func writeTestScaffold(t *testing.T, version string, values map[string]any) string {
	t.Helper()
	root := t.TempDir()
	err := blueprint.WriteScaffold(root, blueprint.Scaffold{
		SchemaVersion: blueprint.ScaffoldSchemaVersion,
		Template:      "test-blueprint",
		Owner:         "o",
		Repo:          "r",
		Version:       version,
		Values:        values,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(blueprint.ScaffoldRelPath))
	loaded, err := blueprint.LoadScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := blueprint.WriteScaffold(root, *loaded); err != nil {
		t.Fatal(err)
	}
	return root
}

func defaultScaffoldValues() map[string]any {
	return map[string]any{
		"name":         "Orders",
		"appPort":      5001,
		"customerName": "Entrovia",
	}
}

func baseOptions(root string, srv *httptest.Server) CreateOptions {
	return CreateOptions{
		StartDir:      root,
		SetValues:     map[string]any{"imageRepository": "ghcr.io/acme/orders"},
		NoInput:       true,
		Stderr:        &bytes.Buffer{},
		HTTP:          srv.Client(),
		GitHubBaseURL: srv.URL,
	}
}

func parseYAMLFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, string(data))
	}
	return doc
}

func TestCreateRendersKustomizeTree(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	if err := Create(context.Background(), baseOptions(root, srv)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	deployDir := filepath.Join(root, "deploy")
	wantFiles := []string{
		"base/deployment.yaml",
		"base/service.yaml",
		"base/kustomization.yaml",
		"overlays/dev/kustomization.yaml",
		"overlays/dev/pubsub.yaml",
		"overlays/prod/kustomization.yaml",
		"overlays/prod/pubsub.yaml",
	}
	for _, rel := range wantFiles {
		path := filepath.Join(deployDir, filepath.FromSlash(rel))
		doc := parseYAMLFile(t, path)
		if len(doc) == 0 {
			t.Errorf("%s parsed to an empty document", rel)
		}
	}

	dep := parseYAMLFile(t, filepath.Join(deployDir, "base", "deployment.yaml"))
	annotations := dig(t, dep, "spec", "template", "metadata", "annotations")
	if annotations["dapr.io/app-id"] != "orders" {
		t.Errorf("dapr.io/app-id = %v (appId should derive from scaffold name)", annotations["dapr.io/app-id"])
	}
	if annotations["dapr.io/app-port"] != "5001" {
		t.Errorf("dapr.io/app-port = %v (appPort should seed from scaffold)", annotations["dapr.io/app-port"])
	}
	containers := dig(t, dep, "spec", "template", "spec")["containers"].([]any)
	image := containers[0].(map[string]any)["image"]
	if image != "ghcr.io/acme/orders:latest" {
		t.Errorf("image = %v", image)
	}

	pubsub := parseYAMLFile(t, filepath.Join(deployDir, "overlays", "prod", "pubsub.yaml"))
	meta := dig(t, pubsub, "spec")["metadata"].([]any)
	first := meta[0].(map[string]any)
	if _, ok := first["secretKeyRef"]; !ok {
		t.Errorf("prod pubsub connectionString should use secretKeyRef, got %v", first)
	}
	second := meta[1].(map[string]any)
	if second["value"] != "Entrovia" {
		t.Errorf("scaffold namespace not reachable in templates: customer = %v", second["value"])
	}
}

func dig(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := doc
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			t.Fatalf("missing map at %q in %v", key, cur)
		}
		cur = next
	}
	return cur
}

func TestCreateSetOverridesScaffoldValue(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	opts := baseOptions(root, srv)
	opts.SetValues["appPort"] = "8080"
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dep := parseYAMLFile(t, filepath.Join(root, "deploy", "base", "deployment.yaml"))
	annotations := dig(t, dep, "spec", "template", "metadata", "annotations")
	if annotations["dapr.io/app-port"] != "8080" {
		t.Errorf("dapr.io/app-port = %v, want 8080 (--set beats scaffold)", annotations["dapr.io/app-port"])
	}
}

func TestCreateVersionFlagOverridesPin(t *testing.T) {
	srv := newManifestsServer(t, "v9.9.9", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	opts := baseOptions(root, srv)
	opts.Version = "v9.9.9"
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestCreateFindsScaffoldFromNestedDir(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())
	nested := filepath.Join(root, "src", "Process")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	opts := baseOptions(root, srv)
	opts.StartDir = nested
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deploy", "base", "deployment.yaml")); err != nil {
		t.Errorf("default output dir should be <projectRoot>/deploy: %v", err)
	}
}

func TestCreateMissingScaffold(t *testing.T) {
	err := Create(context.Background(), CreateOptions{
		StartDir: t.TempDir(),
		NoInput:  true,
		Stderr:   &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "intropy int create") {
		t.Fatalf("expected actionable missing-scaffold error, got %v", err)
	}
}

func TestCreateMissingManifestsDir(t *testing.T) {
	files := map[string]string{
		"test-blueprint/template.yaml":      "unused",
		"test-blueprint/skeleton/README.md": "unused",
	}
	srv := newManifestsServer(t, "v1.2.3", files)
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	err := Create(context.Background(), baseOptions(root, srv))
	if err == nil || !strings.Contains(err.Error(), "does not include deployment manifest templates") {
		t.Fatalf("expected missing-manifests error, got %v", err)
	}
}

func TestCreateTooNewSchemaVersion(t *testing.T) {
	root := t.TempDir()
	err := blueprint.WriteScaffold(root, blueprint.Scaffold{
		SchemaVersion: blueprint.ScaffoldSchemaVersion + 1,
		Template:      "test-blueprint",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = Create(context.Background(), CreateOptions{StartDir: root, NoInput: true, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "upgrade intropy") {
		t.Fatalf("expected schema-version error, got %v", err)
	}
}

func TestCreateRefusesNonEmptyOutputWithoutForce(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy", "existing.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Create(context.Background(), baseOptions(root, srv))
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected non-empty output error, got %v", err)
	}

	opts := baseOptions(root, srv)
	opts.Force = true
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("Create with --force: %v", err)
	}
}

func TestCreateMissingRequiredParamNoInput(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	opts := baseOptions(root, srv)
	opts.SetValues = map[string]any{} // no imageRepository
	err := Create(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "missing required parameter(s): imageRepository") {
		t.Fatalf("expected missing required parameter error, got %v", err)
	}
}

func TestCreateWritesOutputJSON(t *testing.T) {
	srv := newManifestsServer(t, "v1.2.3", testManifestFiles())
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	var stdout bytes.Buffer
	opts := baseOptions(root, srv)
	opts.OutputJSON = "-"
	opts.Stdout = &stdout
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got CreateResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, stdout.String())
	}
	if got.Template != "test-blueprint" || got.Version != "v1.2.3" {
		t.Errorf("result identity = %q@%q", got.Template, got.Version)
	}
	if !filepath.IsAbs(got.OutputDir) {
		t.Errorf("OutputDir should be absolute: %q", got.OutputDir)
	}
	if got.Values["appId"] != "orders" {
		t.Errorf("values[appId] = %v", got.Values["appId"])
	}
}

func TestCreateRejectsReservedScaffoldParameter(t *testing.T) {
	files := testManifestFiles()
	files["test-blueprint/manifests/template.yaml"] = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: bad
spec:
  parameters:
    type: object
    properties:
      scaffold:
        type: string
`
	srv := newManifestsServer(t, "v1.2.3", files)
	defer srv.Close()
	root := writeTestScaffold(t, "v1.2.3", defaultScaffoldValues())

	err := Create(context.Background(), baseOptions(root, srv))
	if err == nil || !strings.Contains(err.Error(), `reserved parameter "scaffold"`) {
		t.Fatalf("expected reserved-parameter error, got %v", err)
	}
}
