//go:build integration

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
	"sync/atomic"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// The host template: everything in base/, rendered once, with the broker chosen
// by spec.files and scopes taken from the injected model. Deliberately mirrors
// the real template's shape so the test exercises the whole contract.
const initHostTemplateYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-host
spec:
  parameters:
    type: object
    required: [domain, system, namespace]
    properties:
      domain: { type: string }
      system: { type: string }
      namespace: { type: string, default: integrations }
      pubsub: { type: string, default: rabbitmq }
      pubsubName: { type: string, default: pubsub }
  files:
    - path: base/dapr/pubsub-servicebus.yaml.tmpl
      when: '{{ eq .pubsub "servicebus" }}'
    - path: base/dapr/pubsub-rabbitmq.yaml.tmpl
      when: '{{ eq .pubsub "rabbitmq" }}'
`

const initHostComponentYAML = `schemaVersion: 1
kind: shared
name: {{ .gitops.host }}
environments: [{{ range $i, $e := .gitops.environments }}{{ if $i }}, {{ end }}{{ $e }}{{ end }}]
`

const initHostPubsubRabbit = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: {{ .pubsubName }}
spec:
  type: pubsub.rabbitmq
  metadata:
    - name: connectionString
      secretKeyRef:
        name: {{ .system }}-secrets
        key: rabbitmq-connection
scopes:
{{- range .topology.pubsubs }}
{{- range .appIds }}
  - {{ . }}
{{- end }}
{{- end }}
`

const initHostPubsubServiceBus = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: {{ .pubsubName }}
spec:
  type: pubsub.azure.servicebus.topics
`

const initHostSecrets = `apiVersion: v1
kind: Secret
metadata:
  name: {{ .system }}-secrets
stringData:
  rabbitmq-connection: REPLACE-ME-RABBITMQ-CONNECTION-STRING
`

const initHostOverlay = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: {{ .namespace }}
resources:
  - ../../base
`

// The component template: the workload is chosen by spec.files from the block
// kind the topology reported.
const initComponentTemplateYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-component
spec:
  parameters:
    type: object
    required: [name, domain, system]
    properties:
      name: { type: string }
      domain: { type: string }
      system: { type: string }
      appId: { type: string }
      workload: { type: string, default: deployment }
      namespace: { type: string, default: integrations }
      registry: { type: string }
      imageNamespace: { type: string, default: integrations }
  # Local manifest rendering resolves one fixture per connector, so every
  # fixture library declares the local catalog.
  local:
    fixtures: [sftp, http]
  values:
    imageRepo: "{{ .registry }}/{{ .imageNamespace }}/{{ .name }}"
    imageTag: "unpinned"
  files:
    - path: base/cronjob.yaml.tmpl
      when: '{{ eq .workload "cronjob" }}'
    - path: base/deployment.yaml.tmpl
      when: '{{ eq .workload "deployment" }}'
`

const initComponentComponentYAML = `schemaVersion: 1
name: {{ .name }}
images:
  - name: {{ .imageRepo }}
environments: [{{ range $i, $e := .gitops.environments }}{{ if $i }}, {{ end }}{{ $e }}{{ end }}]
`

const initComponentCronJob = `apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{ .appId }}
spec:
  schedule: "REPLACE-ME-CRON-SCHEDULE"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: {{ .appId }}
              image: {{ .imageRepo }}:{{ .imageTag }}
`

const initComponentDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .appId }}
spec:
  template:
    spec:
      containers:
        - name: {{ .appId }}
          image: {{ .imageRepo }}:{{ .imageTag }}
`

func initLibraryEntries() map[string]string {
	return map[string]string{
		"deploy-host/template.yaml":                                        initHostTemplateYAML,
		"deploy-host/skeleton/component.yaml.tmpl":                         initHostComponentYAML,
		"deploy-host/skeleton/base/dapr/pubsub-rabbitmq.yaml.tmpl":         initHostPubsubRabbit,
		"deploy-host/skeleton/base/dapr/pubsub-servicebus.yaml.tmpl":       initHostPubsubServiceBus,
		"deploy-host/skeleton/base/secrets/secrets.yaml.tmpl":              initHostSecrets,
		"deploy-host/skeleton/overlays/{{ .env }}/kustomization.yaml.tmpl": initHostOverlay,

		"deploy-component/template.yaml":                                        initComponentTemplateYAML,
		"deploy-component/skeleton/component.yaml.tmpl":                         initComponentComponentYAML,
		"deploy-component/skeleton/base/cronjob.yaml.tmpl":                      initComponentCronJob,
		"deploy-component/skeleton/base/deployment.yaml.tmpl":                   initComponentDeployment,
		"deploy-component/skeleton/overlays/{{ .env }}/kustomization.yaml.tmpl": initHostOverlay,
	}
}

func buildLibraryTarball(t *testing.T, entries map[string]string) []byte {
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
	return buf.Bytes()
}

// libraryCalls records which endpoints a run actually hit, so a test can prove
// that pinning a version skips the latest-release lookup entirely.
type libraryCalls struct {
	latest  atomic.Int32
	tarball atomic.Int32
}

func newInitLibraryServer(t *testing.T, entries map[string]string, calls *libraryCalls) *httptest.Server {
	t.Helper()
	tarball := buildLibraryTarball(t, entries)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		calls.latest.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	})
	// Two tags, same content: v1.0.0 is what "latest" resolves to, v0.9.0 exists
	// only to be asked for explicitly.
	for _, tag := range []string{"v1.0.0", "v0.9.0"} {
		mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
			calls.tarball.Add(1)
			_, _ = w.Write(tarball)
		})
	}
	return httptest.NewServer(mux)
}

// initFixture is everything Init needs: an empty-but-deployable GitOps origin, a
// topology file so no dotnet runs, a template library over httptest, and a config
// file pointing at the origin.
type initFixture struct {
	gitopsOrigin string
	cacheRoot    string
	topologyFile string
	sourceDir    string
	srv          *httptest.Server
	calls        *libraryCalls
}

func newInitFixture(t *testing.T) initFixture {
	t.Helper()
	return newInitFixtureWith(t, initLibraryEntries())
}

func newInitFixtureWith(t *testing.T, entries map[string]string) initFixture {
	t.Helper()

	// A deployable repository with one unrelated component, so deploy.yaml exists
	// and ListComponents has something in it.
	origin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:   "other/other-system/other-component",
		Environments: []string{"dev", "staging", "prod"},
	})

	topoDir := t.TempDir()
	topoFile := filepath.Join(topoDir, "topology.json")
	if err := os.WriteFile(topoFile, []byte(initTopologyRecord), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("INTROPY_GITOPS_REPO", "")
	gittest.WriteFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: "+origin+"\n")

	calls := &libraryCalls{}
	srv := newInitLibraryServer(t, entries, calls)
	t.Cleanup(srv.Close)

	return initFixture{
		gitopsOrigin: origin,
		cacheRoot:    t.TempDir(),
		topologyFile: topoFile,
		// Empty on purpose: the topology file supplies everything, and a component
		// with no scaffold record must only warn.
		sourceDir: t.TempDir(),
		srv:       srv,
		calls:     calls,
	}
}

func (f initFixture) options(stdout, stderr *bytes.Buffer) manifestRunOptions {
	return manifestRunOptions{
		Domain:        "sales",
		TopologyFile:  f.topologyFile,
		SourceDir:     f.sourceDir,
		CacheRoot:     f.cacheRoot,
		Stdin:         strings.NewReader(""),
		Stdout:        stdout,
		Stderr:        stderr,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: f.srv.URL,
		HTTP:          f.srv.Client(),
	}
}

// cachedCheckoutDir is the shared checkout Init worked in, so a test can assert
// the state it was left in.
func cachedCheckoutDir(t *testing.T, f initFixture) string {
	t.Helper()
	return gitops.CheckoutDir(f.cacheRoot, f.gitopsOrigin)
}

// setPlatform rewrites the origin's deploy.yaml with a platform block.
func setPlatform(t *testing.T, f initFixture, platform string) {
	t.Helper()
	body := strings.Replace(gitopstest.DeployYAML,
		"registry: harbor.intropy.io\n",
		"registry: harbor.intropy.io\nplatform:\n  "+platform,
		1)
	if body == gitopstest.DeployYAML {
		t.Fatal("could not splice a platform block into the fixture deploy.yaml")
	}
	gittest.Commit(t, f.gitopsOrigin, "deploy.yaml", body, "set platform")
}

func loadComponentAt(base, component string) (*gitops.ComponentConfig, error) {
	return gitops.LoadComponentConfig(filepath.Join(base, component))
}

// clone returns a fresh clone of the origin so a test can inspect what was pushed.
func (f initFixture) clone(t *testing.T, ref string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "verify")
	if err := git.Clone(context.Background(), command.ExecRunner{}, f.gitopsOrigin, dir); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "checkout", ref)
	return dir
}

func runInit(t *testing.T, opts manifestRunOptions) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	err := runManifestPipeline(context.Background(), opts)
	return stdout.String(), stderr.String(), err
}

func TestInitScaffoldsTheWholeSystem(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer

	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := "domains/sales/distribution"
	for _, rel := range []string{
		base + "/host/component.yaml",
		base + "/host/base/dapr/pubsub-rabbitmq.yaml",
		base + "/host/base/secrets/secrets.yaml",
		base + "/erp-loader/component.yaml",
		base + "/erp-loader/base/deployment.yaml",
		base + "/extractor/component.yaml",
		base + "/extractor/base/cronjob.yaml",
		base + "/reconciler/base/deployment.yaml",
	} {
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s", rel)
		}
	}

	// The platform default is rabbitmq, so the Service Bus component must not be
	// rendered at all — this is the conditionality the whole design turns on.
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(base+"/host/base/dapr/pubsub-servicebus.yaml"))); err == nil {
		t.Error("the servicebus component was rendered on a rabbitmq platform")
	}
}

// An extractor is scheduled and everything else stays resident. This is what the
// hand-written customer repos already do.
func TestInitWorkloadFollowsTheBlockKind(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := "domains/sales/distribution"
	if got := readTreeFile(t, work, base+"/extractor/base/cronjob.yaml"); !strings.Contains(got, "kind: CronJob") {
		t.Errorf("extractor did not get a CronJob:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(base+"/extractor/base/deployment.yaml"))); err == nil {
		t.Error("the extractor also got a Deployment")
	}
	if got := readTreeFile(t, work, base+"/erp-loader/base/deployment.yaml"); !strings.Contains(got, "kind: Deployment") {
		t.Errorf("loader did not get a Deployment:\n%s", got)
	}
}

// scopes: must list exactly the app-ids the topology says use the broker, which
// is what makes single ownership of the Component safe.
func TestInitScopesComeFromTheTopology(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got := readTreeFile(t, f.clone(t, "manifests-create/sales-distribution-all"),
		"domains/sales/distribution/host/base/dapr/pubsub-rabbitmq.yaml")
	for _, want := range []string{"- erp-loader", "- extractor", "- wms-loader"} {
		if !strings.Contains(got, want) {
			t.Errorf("scopes missing %q:\n%s", want, got)
		}
	}
}

// The image must not be pinned by scaffolding: intropy deploy is the only thing
// that writes a digest, and `latest` is what got the previous attempt deleted.
func TestInitLeavesImagesUnpinned(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got := readTreeFile(t, f.clone(t, "manifests-create/sales-distribution-all"),
		"domains/sales/distribution/extractor/base/cronjob.yaml")
	if !strings.Contains(got, ":unpinned") {
		t.Errorf("image is not the unpinned sentinel:\n%s", got)
	}
	if strings.Contains(got, ":latest") {
		t.Errorf("image was pinned to latest:\n%s", got)
	}
}

func TestInitPlanWritesNothingAndTouchesNoGit(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(stdout.String(), "nothing created (--dry-run)") {
		t.Errorf("stdout = %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "create") {
		t.Errorf("the plan did not list the actions:\n%s", stdout.String())
	}
	// Placeholders are reported from the staging tree, so --plan still answers
	// "what will I have to fill in".
	if !strings.Contains(stdout.String(), "REPLACE-ME-CRON-SCHEDULE") {
		t.Errorf("plan did not report placeholders:\n%s", stdout.String())
	}

	branches := gittest.Run(t, f.gitopsOrigin, "branch", "--list")
	if strings.Contains(branches, "manifests-create") {
		t.Errorf("--plan created a branch: %q", branches)
	}
}

func TestInitIsANoOpOnReRun(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// The second run sees its own branch only if it is merged, so merge it into
	// the default branch the way a reviewer would.
	gittest.Run(t, f.gitopsOrigin, "merge", "--ff-only", "manifests-create/sales-distribution-all")

	out, stderr2, err := runInit(t, f.options(&bytes.Buffer{}, &bytes.Buffer{}))
	if err != nil {
		t.Fatalf("second Init: %v\nstderr: %s", err, stderr2)
	}
	if !strings.Contains(out, "all manifest files already exist; nothing created") {
		t.Errorf("re-run was not a no-op:\n%s", out)
	}
	if strings.Contains(out, "create ") {
		t.Errorf("re-run wanted to create something:\n%s", out)
	}
}

// Leaving the checkout on a feature branch is destructive: the refresh on the
// next Open would reset it to the default branch's remote head.
func TestInitRestoresTheDefaultBranchInTheCache(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	checkout := cachedCheckoutDir(t, f)
	head := strings.TrimSpace(gittest.Run(t, checkout, "rev-parse", "--abbrev-ref", "HEAD"))
	if head != "main" {
		t.Errorf("cached checkout left on %q, want main", head)
	}
}

func TestInitJSONResult(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.OutputFormat = OutputJSON

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var res ManifestCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\n%s", err, stdout.String())
	}
	if res.System != "distribution" || res.Domain != "sales" {
		t.Errorf("result = %+v", res)
	}
	if res.Host != HostDirName {
		t.Errorf("Host = %q", res.Host)
	}
	if res.Template != "o/r@v1.0.0" {
		t.Errorf("Template = %q", res.Template)
	}
	if !res.Applied || res.Branch != "manifests-create/sales-distribution-all" || res.Revision == "" {
		t.Errorf("apply not recorded: %+v", res)
	}
	if len(res.Files) == 0 || len(res.Placeholders) == 0 {
		t.Errorf("files = %d, placeholders = %d", len(res.Files), len(res.Placeholders))
	}
	if strings.Join(res.Components, ",") != "erp-loader,extractor,reconciler" {
		t.Errorf("Components = %v", res.Components)
	}
}

// A platform key flows deploy.yaml → parameter → spec.files condition, and the
// CLI never mentions Azure anywhere.
func TestInitRendersServiceBusOnAzure(t *testing.T) {
	f := newInitFixture(t)
	setPlatform(t, f, "provider: azure\n  pubsub: servicebus\n")

	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := "domains/sales/distribution/host/base/dapr"
	if got := readTreeFile(t, work, base+"/pubsub-servicebus.yaml"); !strings.Contains(got, "azure.servicebus") {
		t.Errorf("servicebus component = %q", got)
	}
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(base+"/pubsub-rabbitmq.yaml"))); err == nil {
		t.Error("the rabbitmq component was rendered on an azure platform")
	}
}

// Without --version the run takes whatever the latest release is, which is fine
// interactively and wrong for anything reproducible.
func TestInitUsesTheLatestReleaseByDefault(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.OutputFormat = OutputJSON

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	if got := f.calls.latest.Load(); got != 1 {
		t.Errorf("latest-release endpoint hit %d times, want 1", got)
	}

	var res ManifestCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Template != "o/r@v1.0.0" {
		t.Errorf("Template = %q, want the resolved latest release", res.Template)
	}
}

// --version pins the template release. The latest-release lookup must be skipped
// entirely, or a moving "latest" could still decide what gets rendered.
func TestInitVersionPinsTheTemplateRelease(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TemplateVersion = "v0.9.0"
	opts.OutputFormat = OutputJSON

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	if got := f.calls.latest.Load(); got != 0 {
		t.Errorf("latest-release endpoint hit %d times with --version set, want 0", got)
	}
	if got := f.calls.tarball.Load(); got != 1 {
		t.Errorf("tarball fetched %d times, want 1 for both templates", got)
	}

	var res ManifestCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Template != "o/r@v0.9.0" {
		t.Errorf("Template = %q, want the pinned tag", res.Template)
	}
	// The reviewer of the pushed branch has to be able to tell which release
	// produced it, so the tag is announced too.
	if !strings.Contains(stderr.String(), "o/r@v0.9.0") {
		t.Errorf("stderr does not name the pinned release:\n%s", stderr.String())
	}
}

func TestInitRejectsAVersionThatDoesNotExist(t *testing.T) {
	f := newInitFixture(t)
	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.TemplateVersion = "v9.9.9"

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected an error for a tag the library does not have")
	}
}

func TestInitRequiresDomainOnAFirstRun(t *testing.T) {
	f := newInitFixture(t)
	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.Domain = ""

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected an error without --domain")
	}
	if !strings.Contains(err.Error(), "--domain is required") {
		t.Errorf("error = %v", err)
	}
}

// The scaffold branch is reviewed as a file list, so a hook that stages files of
// its own would put them in front of a reviewer as this command's work — and into
// the GitOps repository.
func TestInitIsUnaffectedByAPreCommitHookInTheCache(t *testing.T) {
	f := newInitFixture(t)

	// The cache is normally created by the run itself; creating it first is what
	// lets a hook be waiting in it, which is the situation being tested.
	cache := cachedCheckoutDir(t, f)
	if err := git.Clone(context.Background(), command.ExecRunner{}, f.gitopsOrigin, cache); err != nil {
		t.Fatal(err)
	}
	proveHookFires := installPreCommitHook(t, cache, "pwned.txt")
	proveHookFires(t)

	if _, stderr, err := runInit(t, f.options(&bytes.Buffer{}, &bytes.Buffer{})); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr)
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	if _, err := os.Stat(filepath.Join(work, "pwned.txt")); err == nil {
		t.Error("the hook's file reached the scaffold branch")
	}
	files := gittest.Run(t, work, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "pwned.txt") {
		t.Errorf("the scaffold commit contains the hook's file:\n%s", files)
	}
}

// A GitOps repository can carry a symlink — git stores one natively and the
// clone reproduces it — so this is the whole attack, end to end: the repository
// names a destination, and the write follows it out of the checkout.
func TestInitRefusesToWriteThroughASymlinkInTheGitopsRepository(t *testing.T) {
	f := newInitFixture(t)

	outside := filepath.Join(t.TempDir(), "captured.yaml")
	if err := os.WriteFile(outside, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.gitopsOrigin, "domains", "sales", "distribution", "host", "component.yaml")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, f.gitopsOrigin, "add", "--", "domains")
	gittest.Run(t, f.gitopsOrigin, "commit", "--quiet", "-m", "plant a symlink")

	_, _, err := runInit(t, f.options(&bytes.Buffer{}, &bytes.Buffer{}))
	if err == nil {
		t.Fatal("expected Init to refuse the symlinked destination")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention the symlink: %v", err)
	}
	if data, readErr := os.ReadFile(outside); readErr != nil || string(data) != "untouched\n" {
		t.Errorf("the file outside the repository was written: %q (%v)", data, readErr)
	}
}

// --domain becomes a tree segment and part of the branch name, so it has to be a
// single name. The write boundary would refuse the traversal anyway; this is
// where the user finds out which flag was wrong.
func TestInitRejectsADomainThatIsNotOneSegment(t *testing.T) {
	for _, domain := range []string{"../../etc", "sales/orders", "."} {
		f := newInitFixture(t)
		opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
		opts.Domain = domain

		_, _, err := runInit(t, opts)
		if err == nil {
			t.Fatalf("--domain %q was accepted", domain)
		}
		if !strings.Contains(err.Error(), "--domain") {
			t.Errorf("--domain %q: error does not name the flag: %v", domain, err)
		}
	}
}

// After the first run the domain is inferable, which is what makes a re-run
// flag-free.
func TestInitInfersDomainOnARerun(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	gittest.Run(t, f.gitopsOrigin, "merge", "--ff-only", "manifests-create/sales-distribution-all")

	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.Domain = ""
	if _, stderr2, err := runInit(t, opts); err != nil {
		t.Fatalf("second Init without --domain: %v\nstderr: %s", err, stderr2)
	}
}

func TestInitRejectsAnUnknownEnvironment(t *testing.T) {
	f := newInitFixture(t)
	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.Environments = []string{"nope"}

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected an error for an environment deploy.yaml does not define")
	}
}

func TestInitRejectsAMalformedTopologyFile(t *testing.T) {
	f := newInitFixture(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"apiVersion":"topology.intropy.io/v0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.TopologyFile = bad

	if _, _, err := runInit(t, opts); err == nil {
		t.Fatal("expected an error for an unsupported apiVersion")
	}
}

// The generated component.yaml has to satisfy the schema the rest of deploy
// reads, or the tree is unusable the moment it merges.
func TestInitProducesLoadableComponentConfigs(t *testing.T) {
	f := newInitFixture(t)
	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := filepath.Join(work, "domains", "sales", "distribution")

	host, err := loadComponentAt(base, HostDirName)
	if err != nil {
		t.Fatalf("host component.yaml: %v", err)
	}
	if !host.IsShared() {
		t.Errorf("host kind = %q, want shared", host.Kind)
	}

	comp, err := loadComponentAt(base, "extractor")
	if err != nil {
		t.Fatalf("extractor component.yaml: %v", err)
	}
	if len(comp.Images) != 1 || !strings.HasSuffix(comp.Images[0].Name, "/extractor") {
		t.Errorf("images = %+v", comp.Images)
	}
	if !comp.SupportsEnvironment("prod") {
		t.Errorf("environments = %v, want every environment in deploy.yaml", comp.Environments)
	}
}

// End to end: a workspace laid out the customary way needs no --domain at all.
func TestInitInfersDomainFromWorkspaceLayout(t *testing.T) {
	f := newInitFixture(t)

	// integrations/domains/<domain>/<system>/system-host, as sys create leaves it.
	workspace := t.TempDir()
	hostDir := filepath.Join(workspace, "domains", "logistics", "distribution", "system-host")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := template.WriteScaffold(hostDir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "system-host",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		Role:          template.RoleSystemHost,
		Values:        map[string]any{"name": "distribution"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stubRunGraph(t, initTopologyRecord)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Domain = "" // the whole point
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	opts.OutputFormat = OutputJSON

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init without --domain: %v\nstderr: %s", err, stderr.String())
	}

	var res ManifestCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Domain != "logistics" {
		t.Errorf("Domain = %q, want logistics from the workspace layout", res.Domain)
	}
	for _, f := range res.Files {
		if !strings.HasPrefix(f.Rel, "domains/logistics/distribution/") {
			t.Errorf("file written outside the inferred domain: %s", f.Rel)
			break
		}
	}
}

func TestInitSelectsTheNamedSystemInAMultiSystemWorkspace(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "order-flow", "distribution")
	called := stubRunGraph(t, initTopologyRecord)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = "" // force host discovery
	opts.SourceDir = workspace
	opts.System = "distribution"

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	want := filepath.Join(workspace, "domains", "x", "distribution", "system-host")
	if *called != want {
		t.Errorf("graph verb ran on %q, want %q", *called, want)
	}
	// Only the selected system is built: the other host is never touched.
	if strings.Contains(*called, "order-flow") {
		t.Errorf("the wrong host was built: %q", *called)
	}
}

func TestInitWithoutSystemInAMultiSystemWorkspaceIsAnError(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "order-flow", "distribution")
	called := stubRunGraph(t, initTopologyRecord)

	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.TopologyFile = ""
	opts.SourceDir = workspace

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected an error: two systems and nothing saying which")
	}
	if !strings.Contains(err.Error(), "--system") {
		t.Errorf("error should point at --system: %v", err)
	}
	// Nothing was built — discovery must not pay for a graph verb to fail.
	if *called != "" {
		t.Errorf("the graph verb ran anyway, on %q", *called)
	}
}

// Selecting by name and renaming the tree segment are the same flag, so when they
// disagree the caller's name wins — and is told.
func TestInitWarnsWhenSystemRenamesTheTreeSegment(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "distribution")
	stubRunGraph(t, initTopologyRecord) // declares system "distribution"

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	opts.System = "product-distribution"
	opts.OutputFormat = OutputJSON

	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "using \"product-distribution\" as the tree segment") {
		t.Errorf("stderr does not warn about the rename:\n%s", stderr.String())
	}

	var res ManifestCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.System != "product-distribution" {
		t.Errorf("System = %q, want the caller's name", res.System)
	}
}
