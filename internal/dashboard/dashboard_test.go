package dashboard

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/topology"
	"github.com/integrio-intropy/intropy-cli/web"
)

func writeScaffold(t *testing.T, dir, tmpl, version string) {
	t.Helper()
	writeScaffoldRole(t, dir, tmpl, version, "")
}

func writeScaffoldRole(t *testing.T, dir, tmpl, version, role string) {
	t.Helper()
	intropyDir := filepath.Join(dir, ".intropy")
	if err := os.MkdirAll(intropyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roleField := ""
	if role != "" {
		roleField = `"role":"` + role + `",`
	}
	body := `{"schemaVersion":1,"template":"` + tmpl + `","owner":"integrio-intropy","repo":"intropy-templates","version":"` + version + `",` + roleField + `"dataFlow":"both","values":{"appId":"int1"}}` + "\n"
	if err := os.WriteFile(filepath.Join(intropyDir, "scaffold.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// emptyTopo is the stub provider for tests that do not exercise /api/topology.
func emptyTopo(context.Context) ([]topology.Entry, []string) { return nil, nil }

// emptyDeploy is the stub provider for tests that do not exercise /api/deploy.
// It reports nothing rather than an error: a test about the catalog should not
// have to care what the GitOps repository says.
func emptyDeploy(context.Context, integrationSummary) deployState { return deployState{} }

func testHandler(t *testing.T, root string) http.Handler {
	t.Helper()
	return testHandlerWith(t, root, providers{topology: emptyTopo, deploy: emptyDeploy})
}

func testHandlerWithTopo(t *testing.T, root string, topo topologyProvider) http.Handler {
	t.Helper()
	return testHandlerWith(t, root, providers{topology: topo, deploy: emptyDeploy})
}

func testHandlerWithDeploy(t *testing.T, root string, dep deployProvider) http.Handler {
	t.Helper()
	return testHandlerWith(t, root, providers{topology: emptyTopo, deploy: dep})
}

func testHandlerWith(t *testing.T, root string, p providers) http.Handler {
	t.Helper()
	h, err := newHandler(root, "test", p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func post(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func TestHealth(t *testing.T) {
	rec := get(t, testHandler(t, t.TempDir()), "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["status"] != "ok" || body["version"] != "test" {
		t.Errorf("unexpected health body: %v", body)
	}
}

func TestIntegrationsEmptyIsArray(t *testing.T) {
	t.Chdir(t.TempDir())
	rec := get(t, testHandler(t, "."), "/api/integrations")
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestIntegrationsPopulated(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "orders"), "hello-world", "v0.1.6")

	rec := get(t, testHandler(t, "."), "/api/integrations")
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0]["path"] != "orders" || entries[0]["template"] != "hello-world" {
		t.Errorf("unexpected entry: %v", entries[0])
	}
}

// TestIntegrationsTreeFields checks the derived sidebar-tree coordinates:
// the integration is named after the directory carrying .intropy, its system
// is the parent directory and its domain the system's parent. System and
// domain are absent when the respective level would be the workspace root.
func TestIntegrationsTreeFields(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "sales", "erp", "order-intake"), "hello-world", "v0.1.6")
	writeScaffold(t, filepath.Join(tmp, "sales", "erp", "invoice-export"), "hello-world", "v0.1.6")
	writeScaffold(t, filepath.Join(tmp, "erp", "stock-sync"), "hello-world", "v0.1.6")
	writeScaffold(t, filepath.Join(tmp, "standalone"), "hello-world", "v0.1.6")

	rec := get(t, testHandler(t, "."), "/api/integrations")
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}

	type coords struct{ system, domain string }
	got := map[string]coords{}
	for _, e := range entries {
		name, _ := e["name"].(string)
		system, _ := e["system"].(string)
		domain, _ := e["domain"].(string)
		got[name] = coords{system, domain}
	}
	want := map[string]coords{
		"order-intake":   {"erp", "sales"},
		"invoice-export": {"erp", "sales"},
		"stock-sync":     {"erp", ""},
		"standalone":     {"", ""},
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("missing entry named %q, got %v", name, got)
			continue
		}
		if g != w {
			t.Errorf("coords for %q = %+v, want %+v", name, g, w)
		}
	}
}

func TestIntegrationDetail(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	proj := filepath.Join(tmp, "orders")
	writeScaffold(t, proj, "hello-world", "v0.1.6")
	if err := os.WriteFile(filepath.Join(proj, "AGENTS.md"), []byte("# Agents\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := get(t, testHandler(t, "."), "/api/integrations/orders")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var detail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if detail["template"] != "hello-world" {
		t.Errorf("template = %v, want hello-world", detail["template"])
	}
	agents, ok := detail["agents"].(map[string]any)
	if !ok {
		t.Fatalf("expected agents enrichment, got %v", detail["agents"])
	}
	if !strings.Contains(agents["content"].(string), "hello") {
		t.Errorf("agents content missing: %v", agents["content"])
	}
}

// TestFlowComponents checks that /api/flow parses the integration's Dapr
// component YAML into structured, classified components (not filenames).
func TestFlowComponents(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	proj := filepath.Join(tmp, "sales", "orders")
	writeScaffold(t, proj, "hello-world", "v0.1.6")
	comps := filepath.Join(proj, "local", "dapr-components")
	if err := os.MkdirAll(comps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(comps, "pubsub.yaml"),
		[]byte("metadata:\n  name: pubsub\nspec:\n  type: pubsub.in-memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(comps, "orders-in.yaml"),
		[]byte("metadata:\n  name: orders-in\nspec:\n  type: bindings.localstorage\n  metadata:\n    - name: direction\n      value: input\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := get(t, testHandler(t, "."), "/api/flow")
	var nodes []struct {
		Name       string          `json:"name"`
		System     string          `json:"system"`
		DataFlow   string          `json:"dataFlow"`
		Components []DaprComponent `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].System != "sales" {
		t.Errorf("system = %q, want sales", nodes[0].System)
	}
	// The scaffold record's dataFlow flows through the embedded Scaffold.
	if nodes[0].DataFlow != "both" {
		t.Errorf("dataFlow = %q, want both", nodes[0].DataFlow)
	}
	if len(nodes[0].Components) != 2 {
		t.Fatalf("components = %d, want 2: %+v", len(nodes[0].Components), nodes[0].Components)
	}
	// Sorted by name: orders-in before pubsub.
	if c := nodes[0].Components[0]; c.Name != "orders-in" || c.Category != "binding" || c.Direction != "input" {
		t.Errorf("component = %+v, want orders-in/binding/input", c)
	}
	if c := nodes[0].Components[1]; c.Name != "pubsub" || c.Category != "pubsub" || c.Type != "pubsub.in-memory" {
		t.Errorf("component = %+v, want pubsub/pubsub.in-memory/pubsub", c)
	}
}

func TestTopologyEmptyIsArray(t *testing.T) {
	t.Chdir(t.TempDir())
	rec := get(t, testHandler(t, "."), "/api/topology")
	var report map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	topos, ok := report["topologies"].([]any)
	if !ok || len(topos) != 0 {
		t.Errorf("topologies = %v, want empty array", report["topologies"])
	}
	if _, present := report["errors"]; present {
		t.Errorf("errors should be omitted when empty, got %v", report["errors"])
	}
}

// TestTopologyPopulated checks that a topology fetched from a host's graph
// verb is served with its path normalized to the same root-relative
// identifier the integration endpoints use.
func TestTopologyPopulated(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	system := filepath.Join("distribution", "product-distribution")
	writeScaffold(t, filepath.Join(system, "pim-extractor"), "extractor", "v0.1.6")

	provider := func(context.Context) ([]topology.Entry, []string) {
		return []topology.Entry{{
			Path: system,
			Topology: topology.Topology{
				APIVersion: topology.APIVersion,
				Kind:       topology.Kind,
				System:     "product-distribution",
				Components: []topology.Component{{
					Name: "pim-extractor",
					Kind: "extractor",
					Publishes: []topology.Publication{
						{Port: "default", PubSub: "pubsub", Topic: "product-raw"},
					},
				}},
				Topics: []topology.Topic{
					{PubSub: "pubsub", Topic: "product-raw", Contract: "Integrio.Contracts.RawProduct"},
				},
			},
		}}, nil
	}

	rec := get(t, testHandlerWithTopo(t, ".", provider), "/api/topology")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var report struct {
		Topologies []map[string]any `json:"topologies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(report.Topologies) != 1 {
		t.Fatalf("topologies = %d, want 1", len(report.Topologies))
	}
	entry := report.Topologies[0]
	if entry["path"] != "distribution/product-distribution" {
		t.Errorf("path = %v, want distribution/product-distribution", entry["path"])
	}
	if entry["system"] != "product-distribution" {
		t.Errorf("system = %v, want product-distribution", entry["system"])
	}
	comps, _ := entry["components"].([]any)
	if len(comps) != 1 {
		t.Fatalf("components = %v, want 1", entry["components"])
	}
	topics, _ := entry["topics"].([]any)
	if len(topics) != 1 {
		t.Fatalf("topics = %v, want 1", entry["topics"])
	}
}

// TestTopologyErrorsSurfaced checks that a failing host does not hide the
// working ones: its message rides along in the report's errors array.
func TestTopologyErrorsSurfaced(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := func(context.Context) ([]topology.Entry, []string) {
		return nil, []string{"orders-host: graph verb failed: exit status 1"}
	}

	rec := get(t, testHandlerWithTopo(t, ".", provider), "/api/topology")
	var report struct {
		Topologies []any    `json:"topologies"`
		Errors     []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if report.Topologies == nil || len(report.Topologies) != 0 {
		t.Errorf("topologies = %v, want empty array", report.Topologies)
	}
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "orders-host") {
		t.Errorf("errors = %v, want the host failure", report.Errors)
	}
}

// TestTopologyMessageDocs checks that authored message descriptions beside a
// system are served keyed by connector name, that a doc naming an undeclared
// connector is withheld and reported, and that docs are read fresh per
// request — an edit shows up without re-running the cached graph provider.
func TestTopologyMessageDocs(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	system := "product-distribution"
	msgs := filepath.Join(system, "messages")
	if err := os.MkdirAll(msgs, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nformat: csv\ndelimiter: \";\"\n---\nNightly ERP export.\n"
	if err := os.WriteFile(filepath.Join(msgs, "erp.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(msgs, "ghost.md"), []byte("orphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	provider := func(context.Context) ([]topology.Entry, []string) {
		calls++
		return []topology.Entry{{
			Path: system,
			Topology: topology.Topology{
				APIVersion: topology.APIVersion,
				System:     system,
				Connectors: []topology.Connector{{
					Name:      "erp",
					Transport: topology.Transport{Type: "file", SupportsInput: true, SupportsOutput: true},
				}},
			},
		}}, nil
	}
	h := testHandlerWithTopo(t, ".", provider)

	var report struct {
		Topologies []struct {
			MessageDocs map[string]messageDoc `json:"messageDocs"`
		} `json:"topologies"`
		Errors []string `json:"errors"`
	}
	decode := func() {
		t.Helper()
		rec := get(t, h, "/api/topology")
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
		}
		if len(report.Topologies) != 1 {
			t.Fatalf("topologies = %d, want 1", len(report.Topologies))
		}
	}

	decode()
	got := report.Topologies[0].MessageDocs["erp"]
	if got.Format != "csv" || got.Delimiter != ";" || got.Body != "Nightly ERP export." {
		t.Errorf("erp doc = %+v", got)
	}
	if _, ok := report.Topologies[0].MessageDocs["ghost"]; ok {
		t.Error("undeclared connector doc must be withheld")
	}
	if len(report.Errors) != 1 ||
		!strings.Contains(report.Errors[0], "product-distribution/messages/ghost.md") ||
		!strings.Contains(report.Errors[0], `no connector "ghost"`) {
		t.Errorf("errors = %v, want one undeclared-connector error", report.Errors)
	}

	// Edit the doc; a plain GET (no refresh) serves the new content from the
	// cached provider result.
	if err := os.WriteFile(filepath.Join(msgs, "erp.md"), []byte("---\nformat: xml\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	decode()
	if got := report.Topologies[0].MessageDocs["erp"]; got.Format != "xml" {
		t.Errorf("edited doc format = %q, want xml", got.Format)
	}
	if calls != 1 {
		t.Errorf("provider calls = %d, want 1 (docs must not invalidate the cache)", calls)
	}
}

// TestTopologyCachedUntilRefresh checks that the provider — which runs a
// dotnet build per host — is called once for any number of GETs, and again
// only on an explicit refresh.
func TestTopologyCachedUntilRefresh(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	provider := func(context.Context) ([]topology.Entry, []string) {
		calls++
		return nil, nil
	}
	h := testHandlerWithTopo(t, ".", provider)

	get(t, h, "/api/topology")
	get(t, h, "/api/topology")
	if calls != 1 {
		t.Fatalf("provider calls after two GETs = %d, want 1", calls)
	}
	if rec := post(t, h, "/api/topology/refresh"); rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("provider calls after refresh = %d, want 2", calls)
	}
	get(t, h, "/api/topology")
	if calls != 2 {
		t.Fatalf("provider calls after post-refresh GET = %d, want 2", calls)
	}
}

// writeSystemHost writes a system-host scaffold declaring a system name, the
// record `sys create` leaves in the rendered host directory.
func writeSystemHost(t *testing.T, dir, name string) {
	t.Helper()
	intropyDir := filepath.Join(dir, ".intropy")
	if err := os.MkdirAll(intropyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":1,"template":"system-host","owner":"integrio-intropy","repo":"intropy-templates","version":"v0.2.0","role":"system-host","values":{"name":"` + name + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(intropyDir, "scaffold.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationsSystemFromHost checks that components scaffolded as
// siblings of a system host — the layout `sys create` produces — are grouped
// under the host's declared system name, even directly under the workspace
// root where no folder-derived system exists.
func TestIntegrationsSystemFromHost(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-extractor"), "extractor", "v0.2.0")
	writeScaffold(t, filepath.Join(tmp, "order-loader"), "loader", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "system-host"), "order-flow")

	rec := get(t, testHandler(t, "."), "/api/integrations")
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (host filtered)", len(entries))
	}
	for _, e := range entries {
		if e["system"] != "order-flow" {
			t.Errorf("system for %v = %v, want order-flow", e["name"], e["system"])
		}
		if _, present := e["domain"]; present {
			t.Errorf("domain for %v = %v, want absent at workspace root", e["name"], e["domain"])
		}
	}
}

// TestIntegrationsSystemFromHostNested checks that a declared system name
// overrides the folder-derived one, and the domain remains folder-derived.
func TestIntegrationsSystemFromHostNested(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "sales", "erp", "order-intake"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "sales", "erp", "system-host"), "erp-sync")

	rec := get(t, testHandler(t, "."), "/api/integrations")
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0]["system"] != "erp-sync" || entries[0]["domain"] != "sales" {
		t.Errorf("coords = %v/%v, want erp-sync/sales", entries[0]["system"], entries[0]["domain"])
	}
}

// TestIntegrationsFilterSupportProjects checks that the system host and the
// shared contracts library are infrastructure, not catalog entries: they are
// absent from listings and their paths do not resolve to a detail view.
func TestIntegrationsFilterSupportProjects(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "pim-extractor"), "extractor", "v0.1.6")
	writeScaffoldRole(t, filepath.Join(tmp, "orders-system-host"), "system-host", "v0.1.6", "system-host")
	writeScaffoldRole(t, filepath.Join(tmp, "orders-contracts"), "contracts", "v0.1.6", "shared-library")

	for _, path := range []string{"/api/integrations", "/api/flow"} {
		rec := get(t, testHandler(t, "."), path)
		var entries []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
			t.Fatalf("%s: body not JSON: %v\n%s", path, err, rec.Body.String())
		}
		if len(entries) != 1 || entries[0]["name"] != "pim-extractor" {
			t.Errorf("%s: entries = %v, want just pim-extractor", path, entries)
		}
	}

	if rec := get(t, testHandler(t, "."), "/api/integrations/orders-system-host"); rec.Code != http.StatusNotFound {
		t.Errorf("host detail status = %d, want 404", rec.Code)
	}
}

// TestIntegrationDetailRoot covers an integration scaffolded at the
// workspace root: its identifier is "." but a browser strips that segment,
// so /api/integrations/ (empty path) must resolve to the root integration.
func TestIntegrationDetailRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, tmp, "transactional", "v0.1.6")

	rec := get(t, testHandler(t, "."), "/api/integrations/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if detail["path"] != "." || detail["template"] != "transactional" {
		t.Errorf("unexpected detail: %v", detail)
	}
}

func TestIntegrationDetailNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rec := get(t, testHandler(t, "."), "/api/integrations/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	h := testHandler(t, t.TempDir())
	for _, path := range []string{"/", "/some/deep/route"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: content-type = %q, want text/html", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "Intropy Dashboard") {
			t.Errorf("%s: body is not index.html", path)
		}
	}
}

// A missing file must 404, not fall back to index.html. Answering a script
// request with HTML and a 200 fails the browser's MIME check and surfaces only
// as a blank page — the failure mode when a binary is built without `make web`.
func TestMissingAssetIsNotFound(t *testing.T) {
	// Names that cannot exist in either build state, so this holds whether or
	// not `make web` has run.
	h := testHandler(t, t.TempDir())
	for _, path := range []string{
		"/assets/index-nosuchbundle.js",
		"/assets/index-nosuchbundle.css",
		"/nested/deep/missing.js",
	} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (body: %.40q)", path, rec.Code, rec.Body.String())
		}
	}
}

// Whatever dist/index.html the binary embeds — the committed placeholder or a
// real `vite build` — every asset it links to must also be embedded. The
// committed placeholder links to none; a real build links to bundles it just
// wrote. Committing a built index.html breaks this: dist/assets is gitignored,
// so the links survive but the files do not, and the dashboard renders blank.
func TestEmbeddedIndexAssetsExist(t *testing.T) {
	index, err := fs.ReadFile(web.Assets, "dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(index), -1)
	for _, ref := range refs {
		name := strings.TrimPrefix(ref[1], "/")
		if _, err := fs.Stat(web.Assets, "dist/"+name); err != nil {
			t.Errorf("dist/index.html references %s but it is not embedded; "+
				"run `make web` to build it, or `make web-clean` to restore the "+
				"placeholder before committing", ref[1])
		}
	}
}
