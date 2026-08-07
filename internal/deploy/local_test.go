package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"gopkg.in/yaml.v3"
)

// The fixture templates deliberately mirror the real templates' local-render
// contract: a spec.local.fixtures catalog on deploy-component, image: <name>
// with no registry prefix or tag, an overlay directory per environment, and
// component.yaml behind a spec.files rule so the local filter can exclude it.
const localHostTemplateYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-host
spec:
  parameters:
    type: object
    required: [system]
    properties:
      system: { type: string }
      pubsub: { type: string, default: redis }
      pubsubName: { type: string, default: pubsub }
  files:
    - path: component.yaml.tmpl
      when: '{{ eq .pubsub "never" }}'
`

const localHostComponentYAML = `schemaVersion: 1
kind: shared
name: {{ .gitops.host }}
`

const localHostPubsub = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: {{ .pubsubName }}
spec:
  type: pubsub.{{ .pubsub }}
`

const localOverlay = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`

const localComponentTemplateYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-component
spec:
  parameters:
    type: object
    required: [name, system]
    properties:
      name: { type: string }
      system: { type: string }
      appId: { type: string }
      workload: { type: string, default: deployment }
      registry: { type: string, default: dev }
  local:
    fixtures: [sftp, http]
  files:
    - path: component.yaml.tmpl
      when: '{{ eq .workload "never" }}'
`

const localComponentComponentYAML = `schemaVersion: 1
name: {{ .name }}
`

const localComponentDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .appId }}
spec:
  template:
    spec:
      containers:
        - name: {{ .appId }}
          image: {{ .name }}
`

func localLibraryEntries() map[string]string {
	return map[string]string{
		"deploy-host/template.yaml":                                        localHostTemplateYAML,
		"deploy-host/skeleton/component.yaml.tmpl":                         localHostComponentYAML,
		"deploy-host/skeleton/base/dapr/pubsub.yaml.tmpl":                  localHostPubsub,
		"deploy-host/skeleton/overlays/{{ .env }}/kustomization.yaml.tmpl": localOverlay,

		"deploy-component/template.yaml":                                        localComponentTemplateYAML,
		"deploy-component/skeleton/component.yaml.tmpl":                         localComponentComponentYAML,
		"deploy-component/skeleton/base/deployment.yaml.tmpl":                   localComponentDeployment,
		"deploy-component/skeleton/overlays/{{ .env }}/kustomization.yaml.tmpl": localOverlay,
	}
}

// localLibraryServer serves one release of the given entries over the
// GitHubBaseURL seam, exactly as the init tests do.
func localLibraryServer(t *testing.T, entries map[string]string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		h := &tar.Header{Name: "owner-repo-abc123/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
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
	tarball := buf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// stubKustomizeBuild swaps the kustomize build for a deterministic walk of the
// staging tree, and records the rendered root kustomization.
func stubKustomizeBuild(t *testing.T) *localRootKustomization {
	t.Helper()
	original := kustomizeBuild
	var got localRootKustomization
	kustomizeBuild = func(_ context.Context, _ command.Runner, dir string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, &got); err != nil {
			return nil, err
		}
		var out bytes.Buffer
		for _, res := range got.Resources {
			overlay := filepath.Join(dir, filepath.FromSlash(res))
			kdata, err := os.ReadFile(filepath.Join(overlay, "kustomization.yaml"))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", res, err)
			}
			out.WriteString("---\n")
			out.Write(kdata)
			base := filepath.Join(overlay, "..", "..", "base")
			entries, err := os.ReadDir(base)
			if err != nil {
				return nil, fmt.Errorf("%s base: %w", res, err)
			}
			for _, e := range entries {
				if e.IsDir() {
					sub, err := os.ReadDir(filepath.Join(base, e.Name()))
					if err != nil {
						return nil, err
					}
					for _, s := range sub {
						data, err := os.ReadFile(filepath.Join(base, e.Name(), s.Name()))
						if err != nil {
							return nil, err
						}
						out.WriteString("---\n")
						out.Write(applyLocalImageOverrides(data, got.Images))
					}
					continue
				}
				data, err := os.ReadFile(filepath.Join(base, e.Name()))
				if err != nil {
					return nil, err
				}
				out.WriteString("---\n")
				out.Write(applyLocalImageOverrides(data, got.Images))
			}
		}
		return out.Bytes(), nil
	}
	t.Cleanup(func() { kustomizeBuild = original })
	return &got
}

// applyLocalImageOverrides mimics what kustomize's images[] does to a rendered
// document: rewrites image: lines whose reference matches an entry's name.
// Without it the stub would leave the pinned `image: <name>` shape untagged
// and trip the guard that the real build satisfies.
func applyLocalImageOverrides(doc []byte, images []localImageEntry) []byte {
	text := string(doc)
	for _, img := range images {
		name := img.NewName
		if name == "" {
			name = img.Name
		}
		text = strings.ReplaceAll(text, "image: "+img.Name+"\n", "image: "+name+":"+img.NewTag+"\n")
	}
	return []byte(text)
}

// localTopologyRecord names two components and two connectors, one of which
// (erp) the state file already binds.
const localTopologyRecord = `{
  "apiVersion": "topology.intropy.io/v1",
  "kind": "SystemTopology",
  "system": "distribution",
  "components": [
    {"name": "erp-loader", "kind": "loader",
     "connectors": [{"connector": "erp", "direction": "out"}]},
    {"name": "extractor", "kind": "extractor",
     "connectors": [{"connector": "price-master", "direction": "in"}]}
  ],
  "connectors": [
    {"name": "erp", "externalSystem": "erp",
     "directions": ["out"], "usedBy": ["erp-loader"]},
    {"name": "price-master", "externalSystem": "price-master",
     "directions": ["in"], "usedBy": ["extractor"]}
  ]
}`

type localFixture struct {
	sourceDir string
	srv       *httptest.Server
}

func newLocalFixture(t *testing.T) localFixture {
	t.Helper()
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "topology.json"), []byte(localTopologyRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	return localFixture{sourceDir: sourceDir, srv: localLibraryServer(t, localLibraryEntries())}
}

func (f localFixture) options(stdout, stderr *bytes.Buffer) LocalOptions {
	return LocalOptions{
		System:        "distribution",
		TopologyFile:  filepath.Join(f.sourceDir, "topology.json"),
		SourceDir:     f.sourceDir,
		NoInput:       true,
		Stdin:         strings.NewReader(""),
		Stdout:        stdout,
		Stderr:        stderr,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	}
}

func writeLocalYAML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".intropy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".intropy", "local.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRendersTheWholeSystem(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	if err := Local(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Local: %v\nstderr: %s", err, stderr.String())
	}

	wantResources := []string{"host/overlays/local", "erp-loader/overlays/local", "extractor/overlays/local"}
	if strings.Join(root.Resources, ",") != strings.Join(wantResources, ",") {
		t.Errorf("resources = %v, want %v", root.Resources, wantResources)
	}
	if root.Namespace != "distribution" {
		t.Errorf("namespace = %q, want the system name", root.Namespace)
	}

	// One images entry per component, host excluded, in the pinned shape.
	if len(root.Images) != 2 {
		t.Fatalf("images = %+v, want 2 entries", root.Images)
	}
	for _, img := range root.Images {
		if img.Name == "host" {
			t.Errorf("the host got an images entry: %+v", img)
		}
		if img.Name != img.NewName && img.NewName != "" {
			t.Errorf("unexpected newName: %+v", img)
		}
		if img.NewTag != "dev" {
			t.Errorf("image %s newTag = %q, want dev", img.Name, img.NewTag)
		}
	}

	// component.yaml is repo metadata and must never reach a kubectl stream.
	if strings.Contains(stdout.String(), "schemaVersion") {
		t.Errorf("component.yaml was rendered into the manifest stream:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "pubsub.redis") {
		t.Errorf("the host pubsub component did not render with the local platform constants:\n%s", stdout.String())
	}
}

func TestLocalNamespaceFlagOverridesTheDefault(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Namespace = "team-a"
	if err := Local(context.Background(), opts); err != nil {
		t.Fatalf("Local: %v", err)
	}
	if root.Namespace != "team-a" {
		t.Errorf("namespace = %q, want team-a", root.Namespace)
	}
}

func TestLocalImageOverrides(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Images = []string{":1.4.0-rc.3", "erp-loader=registry.local/erp-loader:2.0.0"}
	if err := Local(context.Background(), opts); err != nil {
		t.Fatalf("Local: %v", err)
	}
	byName := map[string]localImageEntry{}
	for _, img := range root.Images {
		byName[img.Name] = img
	}
	if got := byName["extractor"]; got.NewTag != "1.4.0-rc.3" || got.NewName != "" {
		t.Errorf("extractor = %+v, want a bare retag to the rc", got)
	}
	if got := byName["erp-loader"]; got.NewName != "registry.local/erp-loader" || got.NewTag != "2.0.0" {
		t.Errorf("erp-loader = %+v, want the explicit name:tag", got)
	}
}

func TestLocalRejectsBadImageGrammar(t *testing.T) {
	for _, arg := range []string{"erp-loader", "erp-loader=nocolon", "=name:tag", "erp-loader=:", ":", "erp-loader=a:b:c"} {
		t.Run(arg, func(t *testing.T) {
			if _, err := parseImageOverrides([]string{arg}); err == nil {
				t.Errorf("--image %q: expected a usage error", arg)
			} else if !strings.Contains(err.Error(), "--image <component>=<name:tag>") {
				t.Errorf("--image %q: error should show both forms: %v", arg, err)
			}
		})
	}
}

func TestLocalImageOverrideNamesAnUnknownComponent(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Images = []string{"ghost=ghost:1.0.0"}
	err := Local(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected an unknown-component error, got %v", err)
	}
}

// A recorded choice is never re-prompted, and an unbound connector under
// --no-input fails with the two-line error naming the file and the remedy.
func TestLocalNoInputFailsOnAnUnboundConnector(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n")

	var stdout, stderr bytes.Buffer
	err := Local(context.Background(), f.options(&stdout, &stderr))
	if err == nil {
		t.Fatal("expected an unbound-connector error")
	}
	if !strings.Contains(err.Error(), "connector price-master has no local binding in") {
		t.Errorf("error should front-load the unbound connector: %v", err)
	}
	if !strings.Contains(err.Error(), "run 'intropy int local distribution' interactively, or add it to the file") {
		t.Errorf("error should name the remedy on a second line: %v", err)
	}
}

func TestLocalPromptsForAnUnboundConnectorAndRecordsIt(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.NoInput = false
	opts.Stdin = strings.NewReader("2\n") // the menu is [sftp, http]: 2 is http
	if err := Local(context.Background(), opts); err != nil {
		t.Fatalf("Local: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "connector price-master (external system price-master) — which binding?") {
		t.Errorf("the prompt did not name the connector and its external system:\n%s", stderr.String())
	}

	cfg, err := loadLocalConfig(localConfigPath(f.sourceDir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bindings["price-master"] != "http" || cfg.Bindings["erp"] != "sftp" {
		t.Errorf("local.yaml = %v, want erp kept and price-master recorded", cfg.Bindings)
	}
}

func TestLocalRejectsARecordedValueOutsideTheCatalog(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: pigeon\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	err := Local(context.Background(), f.options(&stdout, &stderr))
	if err == nil {
		t.Fatal("expected a catalog validation error")
	}
	for _, want := range []string{"erp", "pigeon", "sftp, http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestLocalNotesAStaleBinding(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n  crm: http\n")

	var stdout, stderr bytes.Buffer
	if err := Local(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Local: %v", err)
	}
	if !strings.Contains(stderr.String(), "note: local.yaml binds crm, which the topology no longer declares") {
		t.Errorf("stderr should note the stale entry:\n%s", stderr.String())
	}
}

func TestLocalErrorsWhenTheLibraryHasNoCatalog(t *testing.T) {
	entries := localLibraryEntries()
	entries["deploy-component/template.yaml"] = strings.Replace(
		localComponentTemplateYAML, "  local:\n    fixtures: [sftp, http]\n", "", 1)
	f := newLocalFixture(t)
	f.srv.Close()
	f.srv = localLibraryServer(t, entries)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	err := Local(context.Background(), f.options(&stdout, &stderr))
	if err == nil {
		t.Fatal("expected a missing-catalog error")
	}
	if !strings.Contains(err.Error(), "declares no fixture catalog") || !strings.Contains(err.Error(), "--template-version") {
		t.Errorf("error should name the library and the escape hatch: %v", err)
	}
}

// The untagged-image guard exists because kustomize silently ignores an
// images[] entry that matches nothing — template drift must fail at render
// time, not at pull time.
func TestLocalFailsWhenADeploymentImageHasNoTag(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: extractor
spec:
  template:
    spec:
      containers:
        - name: extractor
          image: extractor
`)
	if err := assertAllImagesTagged(manifest); err == nil {
		t.Fatal("expected an untagged-image error")
	} else if !strings.Contains(err.Error(), "without a tag") {
		t.Errorf("error: %v", err)
	}

	tagged := strings.Replace(string(manifest), "image: extractor\n", "image: extractor:dev\n", 1)
	if err := assertAllImagesTagged([]byte(tagged)); err != nil {
		t.Errorf("tagged image should pass: %v", err)
	}
}

// Host discovery through selectHost, the way the init tests exercise it: no
// topology file, so the runGraph seam supplies the record for the one host in
// the workspace.
func TestLocalDiscoversTheHostAndRunsTheGraphVerb(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	writeLocalYAML(t, f.sourceDir, "connectors:\n  erp: sftp\n  price-master: http\n")

	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "distribution")
	called := stubRunGraph(t, localTopologyRecord)
	writeLocalYAML(t, workspace, "connectors:\n  erp: sftp\n  price-master: http\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	if err := Local(context.Background(), opts); err != nil {
		t.Fatalf("Local: %v\nstderr: %s", err, stderr.String())
	}
	want := filepath.Join(workspace, "domains", "x", "distribution", "system-host")
	if *called != want {
		t.Errorf("graph verb ran on %q, want %q", *called, want)
	}
}

func TestParseImageOverridesBothForms(t *testing.T) {
	overrides, err := parseImageOverrides([]string{":1.4.0-rc.3", "erp-loader=img:2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 2 {
		t.Fatalf("overrides = %+v", overrides)
	}
	if overrides[0].Component != "" || overrides[0].NewTag != "1.4.0-rc.3" {
		t.Errorf("global form parsed as %+v", overrides[0])
	}
	if overrides[1].Component != "erp-loader" || overrides[1].NewName != "img" || overrides[1].NewTag != "2.0.0" {
		t.Errorf("component form parsed as %+v", overrides[1])
	}
}

func TestLocalConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := localConfigPath(dir)

	// A missing file is an empty config, not an error.
	cfg, err := loadLocalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings) != 0 {
		t.Errorf("missing file gave bindings %v", cfg.Bindings)
	}

	if err := saveLocalConfig(path, localConfig{Bindings: map[string]string{"erp": "sftp", "crm": "http"}}); err != nil {
		t.Fatal(err)
	}
	got, err := loadLocalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bindings["erp"] != "sftp" || got.Bindings["crm"] != "http" {
		t.Errorf("round trip gave %v", got.Bindings)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted keys keep the checked-in file's diffs minimal.
	if strings.Index(string(data), "crm") > strings.Index(string(data), "erp") {
		t.Errorf("keys are not sorted:\n%s", data)
	}
}
