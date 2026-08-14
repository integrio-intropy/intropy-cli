//go:build integration

package deploy

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	f := newInitFixtureWith(t, localTemplateEntries(t, root))

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	if err := runManifestPipeline(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	t.Logf("stdout:\n%s", stdout.String())

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := filepath.Join(work, "domains", "sales", "distribution")

	for _, unit := range []string{"host", "extractor", "erp-loader", "reconciler"} {
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
	f := newInitFixtureWith(t, localTemplateEntries(t, root))

	f.topologyFile = filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(f.topologyFile, []byte(`{
		"apiVersion": "topology.intropy.io/v1",
		"kind": "SystemTopology",
		"system": "distribution",
		"components": [
			{"name": "order-loader", "kind": "loader",
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
	opts := f.options(&stdout, &stderr)
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
	f := newInitFixtureWith(t, localTemplateEntries(t, root))
	setPlatform(t, f, "provider: azure\n  pubsub: servicebus\n  secretStore: azure-keyvault\n")

	var stdout, stderr bytes.Buffer
	if err := runManifestPipeline(context.Background(), f.options(&stdout, &stderr)); err != nil {
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
