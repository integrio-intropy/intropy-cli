//go:build integration

package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// localTemplateEntries reads the real deploy-host / deploy-component templates
// out of a local intropy-templates checkout, so a run exercises the shipped
// content rather than a fixture.
func localTemplateEntries(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	for _, name := range []string{"deploy-host", "deploy-component"} {
		dir := filepath.Join(root, name)
		if _, err := os.Stat(dir); err != nil {
			t.Skipf("no %s in %s", name, root)
		}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			// Only what the renderer reads.
			slash := filepath.ToSlash(rel)
			if !strings.HasSuffix(slash, "/template.yaml") && !strings.Contains(slash, "/skeleton/") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entries[slash] = string(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return entries
}

// localTemplatesRoot gates these tests on a local intropy-templates checkout.
//
// Skipped by default: the shipped template content lives in another repository,
// so this is an authoring aid rather than something CI can run. No network is
// involved — the templates are served from a tarball built in memory.
//
// Always pass -count=1. Go caches test results by package inputs, and the
// templates directory is not one of them, so an edit over there replays the
// previous run's output and looks like the change had no effect.
func localTemplatesRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("INTROPY_TEMPLATES_DIR")
	if root == "" {
		t.Skip("set INTROPY_TEMPLATES_DIR to a local intropy-templates checkout (and pass -count=1)")
	}
	return root
}

func TestLocalTemplatesRenderAndBuild(t *testing.T) {
	requireKustomize(t)
	root := localTemplatesRoot(t)
	f := newRealTemplatesInitFixture(t, root)

	var stdout, stderr bytes.Buffer
	opts := f.options(t, &stdout, &stderr)
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	t.Logf("stdout:\n%s", stdout.String())

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := filepath.Join(work, "domains", "sales", "distribution")

	for _, unit := range []string{"host", "extractor", "erp-loader", "reconciler"} {
		if _, err := os.Stat(filepath.Join(base, unit, "base", "service.yaml")); !os.IsNotExist(err) {
			t.Errorf("non-gateway %s has an application Service, stat error: %v", unit, err)
		}
		for _, env := range []string{"dev", "staging", "prod"} {
			overlay := filepath.Join(base, unit, "overlays", env)
			out, err := exec.Command("kustomize", "build", overlay).CombinedOutput()
			if err != nil {
				t.Errorf("kustomize build %s/%s failed: %v\n%s", unit, env, err, out)
				continue
			}
			t.Logf("=== %s/%s ===\n%s", unit, env, out)
		}
	}

	// The generated component.yaml has to satisfy the schema the rest of deploy
	// reads, or the tree is unusable the moment it merges.
	host, err := loadComponentAt(base, HostDirName)
	if err != nil {
		t.Fatalf("host component.yaml: %v", err)
	}
	if !host.IsShared() {
		t.Errorf("host kind = %q", host.Kind)
	}
	comp, err := loadComponentAt(base, "extractor")
	if err != nil {
		t.Fatalf("extractor component.yaml: %v", err)
	}
	if len(comp.Images) != 1 {
		t.Errorf("images = %+v", comp.Images)
	}

	// The fixture selects erp=sftp and price-master=http (see initFixture's
	// options), so the host renders exactly those two adapter files.
	for _, want := range []struct{ path, bindingType, port string }{
		{"domains/sales/distribution/host/base/bindings/sftp.yaml", "bindings.sftp", "erp"},
		{"domains/sales/distribution/host/base/bindings/http.yaml", "bindings.http", "price-master"},
	} {
		got := readTreeFile(t, work, want.path)
		if !strings.Contains(got, "type: "+want.bindingType) {
			t.Errorf("%s missing type %q:\n%s", want.path, want.bindingType, got)
		}
		if !strings.Contains(got, "name: "+want.port) {
			t.Errorf("%s missing port %q:\n%s", want.path, want.port, got)
		}
	}
	for _, absent := range []string{"file.yaml", "blob.yaml"} {
		if _, err := os.Stat(filepath.Join(work, "domains/sales/distribution/host/base/bindings", absent)); !os.IsNotExist(err) {
			t.Errorf("unselected adapter %s was rendered, stat err = %v", absent, err)
		}
	}
}

// A GitOps binding kind selects the adapter's spec.type; its address and
// credentials stay REPLACE-ME GitOps configuration for the reviewer.
func TestLocalTemplatesRenderSelectedBindingAdapters(t *testing.T) {
	requireKustomize(t)
	root := localTemplatesRoot(t)
	f := newRealTemplatesInitFixture(t, root)

	f.topologyFile = filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(f.topologyFile, []byte(`{
		"apiVersion": "topology.intropy.io/v1",
		"kind": "SystemTopology",
		"system": "distribution",
		"components": [
			{"name": "order-loader", "kind": "loader",
			 "channel": {"pubsub": "distribution-pubsub", "topic": "orders"},
			 "subscribes": [{"pubsub":"distribution-pubsub", "topic":"orders", "resource":"order-loader-subscription", "defaultPath":"/unhandled"}],
			 "ports": [{"port": "erp", "direction": "out"}]}
		],
		"ports": [
			{"name": "erp",
			 "directions": ["out"], "usedBy": ["order-loader"]}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	opts := f.options(t, &stdout, &stderr)
	opts.Bindings = []string{"erp=sftp"}
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	got := readTreeFile(t, work, "domains/sales/distribution/host/base/bindings/sftp.yaml")
	for _, want := range []string{
		"name: erp",
		"type: bindings.sftp",
		"  - order-loader",
		"REPLACE-ME-SFTP-HOST",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered sftp.yaml missing %q\n%s", want, got)
		}
	}
}

func TestLocalTemplatesAzureRendersServiceBusAndNoSecrets(t *testing.T) {
	requireKustomize(t)
	root := localTemplatesRoot(t)
	f := newRealTemplatesInitFixture(t, root)
	setPlatform(t, f, "provider: azure\n  pubsub: servicebus\n  secretStore: azure-keyvault\n")

	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(t, &stdout, &stderr)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	hostBase := filepath.Join(work, "domains", "sales", "distribution", "host", "base")

	if _, err := os.Stat(filepath.Join(hostBase, "dapr", "pubsub-servicebus.yaml")); err != nil {
		t.Errorf("servicebus component missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hostBase, "dapr", "pubsub-rabbitmq.yaml")); err == nil {
		t.Error("rabbitmq component rendered on azure")
	}
	// With Key Vault the values live there, so committing a placeholder Secret
	// would be misleading — the whole directory must be absent.
	if _, err := os.Stat(filepath.Join(hostBase, "secrets")); err == nil {
		t.Error("base/secrets was rendered despite an external secret store")
	}

	out, err := exec.Command("kustomize", "build", filepath.Join(work, "domains", "sales", "distribution", "host", "overlays", "prod")).CombinedOutput()
	if err != nil {
		t.Fatalf("kustomize build failed: %v\n%s", err, out)
	}
	t.Logf("azure host/prod:\n%s", out)
}

const gatewayTopologyRecord = `{
  "apiVersion":"topology.intropy.io/v1", "kind":"SystemTopology", "system":"distribution",
  "components":[{"name":"order-gateway","kind":"gateway",
    "endpoints":[{"name":"orders-endpoint","path":"/api/orders"},{"name":"edi-endpoint","path":"/edi/orders"}],
    "publishes":[{"pubsub":"distribution-pubsub","topic":"orders"}],
    "channel":{"pubsub":"distribution-pubsub","topic":"orders"}}],
  "topics":[{"pubsub":"distribution-pubsub","topic":"orders","publishers":["order-gateway"]}]
}`

func TestLocalTemplatesGatewayServiceAndProbes(t *testing.T) {
	requireKustomize(t)
	f := newLocalFixtureWith(t, localTemplateEntries(t, localTemplatesRoot(t)))
	if err := os.WriteFile(filepath.Join(f.sourceDir, "topology.json"), []byte(gatewayTopologyRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	opts := f.renderOptions(&stderr)
	opts.Bindings = nil
	built, err := RenderManifests(t.Context(), opts)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, stderr.String())
	}
	assertGatewayResources(t, built)
}

func TestLocalTemplatesGatewayProductionOverlays(t *testing.T) {
	requireKustomize(t)
	f := newRealTemplatesInitFixture(t, localTemplatesRoot(t))
	if err := os.WriteFile(f.topologyFile, []byte(gatewayTopologyRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	opts := f.options(t, &stdout, &stderr)
	opts.Bindings = nil
	if err := runManifestPipeline(t.Context(), opts); err != nil {
		t.Fatalf("render: %v\n%s", err, stderr.String())
	}
	work := f.clone(t, "manifests-create/sales-distribution-all")
	for _, env := range []string{"dev", "staging", "prod"} {
		t.Run(env, func(t *testing.T) {
			overlay := filepath.Join(work, "domains", "sales", "distribution", "order-gateway", "overlays", env)
			built, err := exec.Command("kustomize", "build", overlay).CombinedOutput()
			if err != nil {
				t.Fatalf("kustomize: %v\n%s", err, built)
			}
			assertGatewayResources(t, built)
		})
	}
}

func assertGatewayResources(t *testing.T, built []byte) {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(built))
	var service, deployment map[string]any
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch doc["kind"] {
		case "Service":
			service = doc
		case "Deployment":
			deployment = doc
		case "Ingress":
			t.Fatal("public ingress must be explicitly configured by the environment")
		}
	}
	if service == nil || deployment == nil {
		t.Fatalf("missing gateway Service or Deployment:\n%s", built)
	}
	metadata := service["metadata"].(map[string]any)
	if metadata["name"] != "order-gateway" {
		t.Fatalf("service name = %v", metadata["name"])
	}
	spec := service["spec"].(map[string]any)
	if spec["type"] != "ClusterIP" {
		t.Fatalf("service must stay private: %#v", spec)
	}
	if spec["selector"].(map[string]any)["app"] != "order-gateway" {
		t.Fatalf("selector = %#v", spec["selector"])
	}
	port := spec["ports"].([]any)[0].(map[string]any)
	if port["name"] != "http" || port["port"] != 80 || port["targetPort"] != "http" {
		t.Fatalf("service port = %#v", port)
	}
	var endpoints []map[string]string
	annotations := metadata["annotations"].(map[string]any)
	if err := json.Unmarshal([]byte(annotations["intropy.io/endpoints"].(string)), &endpoints); err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0]["path"] != "/edi/orders" || endpoints[1]["path"] != "/api/orders" {
		t.Fatalf("endpoint contract = %#v", endpoints)
	}
	pod := deployment["spec"].(map[string]any)["template"].(map[string]any)
	if pod["metadata"].(map[string]any)["annotations"].(map[string]any)["dapr.io/app-protocol"] != "http" {
		t.Fatal("gateway sidecar must use HTTP")
	}
	container := pod["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	for probe, path := range map[string]string{"readinessProbe": "/readyz", "livenessProbe": "/healthz"} {
		http := container[probe].(map[string]any)["httpGet"].(map[string]any)
		if http["path"] != path || http["port"] != "http" {
			t.Fatalf("%s = %#v", probe, http)
		}
	}
}

func TestLocalTemplatesGatewayRequiresEndpointAndChannelFacts(t *testing.T) {
	entries := localTemplateEntries(t, localTemplatesRoot(t))
	for _, missing := range []string{"endpoints", "pubsub", "topic"} {
		t.Run(missing, func(t *testing.T) {
			var record map[string]any
			if err := json.Unmarshal([]byte(gatewayTopologyRecord), &record); err != nil {
				t.Fatal(err)
			}
			component := record["components"].([]any)[0].(map[string]any)
			if missing == "endpoints" {
				delete(component, "endpoints")
			} else {
				delete(component["channel"].(map[string]any), missing)
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			f := newLocalFixtureWith(t, entries)
			if err := os.WriteFile(filepath.Join(f.sourceDir, "topology.json"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			opts := f.renderOptions(&stderr)
			opts.Bindings = nil
			_, err = RenderManifests(t.Context(), opts)
			want := map[string]string{"endpoints": "names no endpoints", "pubsub": "names no pub/sub", "topic": "names no topic"}[missing]
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

// Real-template renders use the current channel and Subscription contract;
// the older minimal graph fixtures separately exercise backward-compatible decoding.
const runtimeTemplateTopologyRecord = `{
  "apiVersion":"topology.intropy.io/v1", "kind":"SystemTopology", "system":"distribution",
  "components":[
    {"name":"erp-loader","kind":"loader",
     "channel":{"pubsub":"price-pubsub","topic":"price-b2b"},
     "subscribes":[{"pubsub":"price-pubsub","topic":"price-b2b","resource":"erp-loader-subscription","defaultPath":"/unhandled"}],
     "ports":[{"port":"erp","direction":"out"}]},
    {"name":"extractor","kind":"extractor",
     "channel":{"pubsub":"price-pubsub","topic":"price-b2b"},
     "publishes":[{"pubsub":"price-pubsub","topic":"price-b2b"}],
     "ports":[{"port":"price-master","direction":"in"}]},
    {"name":"reconciler","kind":"reconciler"}
  ],
  "topics":[{"pubsub":"price-pubsub","topic":"price-b2b","publishers":["extractor"],"subscribers":["erp-loader"]}],
  "ports":[
    {"name":"price-master","externalSystem":"price-master","directions":["in"],"usedBy":["extractor"]},
    {"name":"erp","externalSystem":"erp","directions":["out"],"usedBy":["erp-loader"]}
  ]
}`

func newRealTemplatesInitFixture(t *testing.T, root string) initFixture {
	t.Helper()
	f := newInitFixtureWith(t, localTemplateEntries(t, root))
	if err := os.WriteFile(f.topologyFile, []byte(runtimeTemplateTopologyRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}
