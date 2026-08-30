package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"gopkg.in/yaml.v3"
)

// The fixture templates deliberately mirror the real templates' local-render
// contract: a spec.local.fixtures catalog on deploy-component, image:
// local/<name> with no tag, an overlay directory per environment, and
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
          image: local/{{ .name }}
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
// Without it the stub would leave the pinned `image: local/<name>` shape untagged
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

// localTopologyRecord names two components and two ports, one of which (erp)
// the state file already binds.
const localTopologyRecord = `{
  "apiVersion": "topology.intropy.io/v1",
  "kind": "SystemTopology",
  "system": "distribution",
  "components": [
    {"name": "erp-loader", "kind": "loader",
     "ports": [{"port": "erp", "direction": "out"}]},
    {"name": "extractor", "kind": "extractor",
     "ports": [{"port": "price-master", "direction": "in"}]}
  ],
  "ports": [
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

func (f localFixture) options(stdout, stderr *bytes.Buffer) manifestRunOptions {
	return manifestRunOptions{
		Mode:          modeLocal,
		System:        "distribution",
		TopologyFile:  filepath.Join(f.sourceDir, "topology.json"),
		SourceDir:     f.sourceDir,
		Stdin:         strings.NewReader(""),
		Bindings:      []string{"erp=sftp", "price-master=http"},
		Stdout:        stdout,
		Stderr:        stderr,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	}
}

type fakeBindingSelector struct {
	choices  map[string]string
	requests []interactive.SelectRequest
}

func (s *fakeBindingSelector) Select(_ context.Context, req interactive.SelectRequest) (string, error) {
	s.requests = append(s.requests, req)
	_, port, found := strings.Cut(req.Title, " binding for ")
	if !found {
		return "", nil
	}
	return s.choices[port], nil
}

func TestLocalRendersTheWholeSystem(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
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
		if !strings.HasPrefix(img.Name, "local/") {
			t.Errorf("image entry %q misses the local/ prefix the rendered reference carries", img.Name)
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
	if !strings.Contains(stdout.String(), "pubsub.rabbitmq") {
		t.Errorf("the host pubsub component did not render with the local platform constants:\n%s", stdout.String())
	}
}

func TestLocalNamespaceFlagOverridesTheDefault(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Namespace = "team-a"
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if root.Namespace != "team-a" {
		t.Errorf("namespace = %q, want team-a", root.Namespace)
	}
}

func TestLocalImageOverrides(t *testing.T) {
	f := newLocalFixture(t)
	root := stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Images = []string{":1.4.0-rc.3", "erp-loader=registry.local/erp-loader:2.0.0"}
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	byName := map[string]localImageEntry{}
	for _, img := range root.Images {
		byName[img.Name] = img
	}
	if got := byName["local/extractor"]; got.NewTag != "1.4.0-rc.3" || got.NewName != "" {
		t.Errorf("extractor = %+v, want a bare retag to the rc", got)
	}
	if got := byName["local/erp-loader"]; got.NewName != "registry.local/erp-loader" || got.NewTag != "2.0.0" {
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

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Images = []string{"ghost=ghost:1.0.0"}
	err := runManifestPipeline(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected an unknown-component error, got %v", err)
	}
}

func TestLocalNonInteractiveRenderFailsForMissingBindings(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Bindings = []string{"erp=sftp"}
	err := runManifestPipeline(context.Background(), opts)
	if err == nil {
		t.Fatal("expected a missing-binding error")
	}
	for _, want := range []string{"local bindings are required for ports: price-master", "--binding <port>=<fixture>", "sftp, http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestLocalRejectsAFixtureOutsideTheCatalog(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Bindings = []string{"erp=pigeon", "price-master=http"}
	err := runManifestPipeline(context.Background(), opts)
	if err == nil {
		t.Fatal("expected a catalog validation error")
	}
	for _, want := range []string{"erp", "pigeon", "sftp, http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestLocalRejectsABindingForAnUnknownPort(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Bindings = append(opts.Bindings, "crm=http")
	err := runManifestPipeline(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), `port "crm"`) {
		t.Fatalf("expected an unknown-port error, got %v", err)
	}
}

func TestLocalPromptsOnlyForMissingBindings(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)
	selector := &fakeBindingSelector{choices: map[string]string{"price-master": "http"}}

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Bindings = []string{"erp=sftp"}
	opts.Selector = selector
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("render with selector: %v", err)
	}
	if len(selector.requests) != 1 || selector.requests[0].Title != "local binding for price-master" {
		t.Fatalf("selector requests = %+v, want price-master only", selector.requests)
	}
	if got := selector.requests[0].Options; len(got) != 2 || got[0].Value != "sftp" || got[1].Value != "http" {
		t.Errorf("selector options = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(f.sourceDir, ".intropy", "deploy-values.yaml")); !os.IsNotExist(err) {
		t.Errorf("local selection was persisted: %v", err)
	}
}

// The label map describes the fixtures the selector knows how to gloss. A
// catalog entry with no description must still render as the bare name — the
// catalog is data from the template and may grow fixtures ahead of this map.
func TestFixtureLabelDescribesKnownFixturesAndFallsBack(t *testing.T) {
	cases := map[string]string{
		"blob": "blob — S3 object store",
		"file": "file — local directory",
		"http": "http — HTTP stub",
		"sftp": "sftp — SFTP server",
		"smb":  "smb — SMB share",
		"nfs":  "nfs",
	}
	for fixture, want := range cases {
		if got := fixtureLabel(fixture); got != want {
			t.Errorf("fixtureLabel(%q) = %q, want %q", fixture, got, want)
		}
	}
}

func TestParsePortBindingArgsRejectsInvalidAndDuplicateValues(t *testing.T) {
	for _, args := range [][]string{{"erp"}, {"=http"}, {"erp="}, {"erp=http", "erp=sftp"}} {
		if _, err := parsePortBindingArgs(args); err == nil {
			t.Errorf("parsePortBindingArgs(%q) succeeded", args)
		}
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

	var stdout, stderr bytes.Buffer
	err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr))
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

	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "distribution")
	called := stubRunGraph(t, localTopologyRecord)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
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

func TestInspectManifestsReportsTheModelAndFixtureCatalog(t *testing.T) {
	f := newLocalFixture(t)

	var stdout, stderr bytes.Buffer
	err := InspectManifests(context.Background(), InspectManifestOptions{
		System:        "distribution",
		SourceDir:     f.sourceDir,
		TopologyFile:  filepath.Join(f.sourceDir, "topology.json"),
		OutputFormat:  OutputJSON,
		Stdout:        &stdout,
		Stderr:        &stderr,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	})
	if err != nil {
		t.Fatalf("InspectManifests: %v\nstderr: %s", err, stderr.String())
	}
	for _, want := range []string{`"system": "distribution"`, `"name": "extractor"`, `"localFixtures": [`, `"sftp"`, `"http"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("inspection does not contain %s:\n%s", want, stdout.String())
		}
	}
}

func TestRenderManifestsReturnsOnlyACompleteBuild(t *testing.T) {
	f := newLocalFixture(t)
	stubKustomizeBuild(t)

	var stderr bytes.Buffer
	built, err := RenderManifests(context.Background(), RenderManifestOptions{
		Environment:   localEnv,
		System:        "distribution",
		SourceDir:     f.sourceDir,
		TopologyFile:  filepath.Join(f.sourceDir, "topology.json"),
		Bindings:      []string{"erp=sftp", "price-master=http"},
		Stderr:        &stderr,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	})
	if err != nil {
		t.Fatalf("RenderManifests: %v\nstderr: %s", err, stderr.String())
	}
	if len(built) == 0 || !strings.Contains(string(built), "kind: Deployment") {
		t.Errorf("rendered YAML = %q", built)
	}
}

func TestRenderManifestsReturnsNoBytesWhenBuildFails(t *testing.T) {
	f := newLocalFixture(t)
	original := kustomizeBuild
	kustomizeBuild = func(context.Context, command.Runner, string) ([]byte, error) {
		return []byte("partial YAML that must not escape\n"), errors.New("document 7 failed")
	}
	t.Cleanup(func() { kustomizeBuild = original })

	built, err := RenderManifests(context.Background(), RenderManifestOptions{
		Environment:   localEnv,
		System:        "distribution",
		SourceDir:     f.sourceDir,
		TopologyFile:  filepath.Join(f.sourceDir, "topology.json"),
		Bindings:      []string{"erp=sftp", "price-master=http"},
		Stderr:        &bytes.Buffer{},
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	})
	if err == nil {
		t.Fatal("expected the build error")
	}
	if built != nil {
		t.Errorf("built = %q, want nil on failure", built)
	}
}

func TestGitOpsFileRulesExcludeLocalOverlay(t *testing.T) {
	rules := gitOpsFileRules([]template.FileRule{{Path: "base/**", When: "true"}})
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(rules))
	}
	if rules[0].Path != "overlays/local/**" || rules[0].When != localExclusionReason {
		t.Errorf("local exclusion = %+v", rules[0])
	}
}
