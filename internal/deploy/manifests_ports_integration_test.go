//go:build integration

package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestCreateRequiresGitOpsBindingsWithoutASelector(t *testing.T) {
	f := newInitFixture(t)
	opts := f.options(t, nil, nil)
	opts.Bindings = nil

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected missing GitOps bindings to fail")
	}
	for _, want := range []string{"gitops bindings are required for ports: erp", "--binding <port>=<kind>", "sftp, http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestManifestCreateRequiresGitOpsBindingCatalog(t *testing.T) {
	entries := initLibraryEntries()
	entries["deploy-host/template.yaml"] = strings.Replace(initHostTemplateYAML, "  gitops:\n    bindingKinds: [sftp, http]\n", "", 1)
	f := newInitFixtureWith(t, entries)

	_, _, err := runInit(t, f.options(t, nil, nil))
	if err == nil || !strings.Contains(err.Error(), "spec.gitops.bindingKinds on deploy-host") {
		t.Fatalf("error = %v", err)
	}
}

func TestManifestCreatePromptsForMissingGitOpsBindings(t *testing.T) {
	f := newInitFixture(t)
	opts := f.options(t, nil, nil)
	opts.Bindings = []string{"erp=sftp"}
	selector := &fakeBindingSelector{choices: map[string]string{"price-master": "http"}}
	opts.Selector = selector

	if _, _, err := runInit(t, opts); err != nil {
		t.Fatalf("manifest create: %v", err)
	}
	if len(selector.requests) != 1 || selector.requests[0].Title != "gitops binding for price-master" {
		t.Errorf("selector requests = %+v", selector.requests)
	}
}

func TestManifestCreateRendersSelectedGitOpsBindingKinds(t *testing.T) {
	entries := initLibraryEntries()
	entries["deploy-host/skeleton/base/bindings/bindings.yaml.tmpl"] = `{{- range .topology.ports }}
---
kind: Component
spec:
  type: {{ if .binding }}bindings.{{ .binding }}{{ else }}REPLACE-ME-BINDING-TYPE{{ end }}
{{- end }}
`
	f := newInitFixtureWith(t, entries)

	if _, _, err := runInit(t, f.options(t, nil, nil)); err != nil {
		t.Fatalf("manifest create: %v", err)
	}

	got := readTreeFile(t, f.clone(t, "manifests-create/sales-distribution-all"),
		"domains/sales/distribution/host/base/bindings/bindings.yaml")
	for _, want := range []string{"type: bindings.sftp", "type: bindings.http"} {
		if !strings.Contains(got, want) {
			t.Errorf("selected binding missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "REPLACE-ME-BINDING-TYPE") {
		t.Errorf("selected binding remained a placeholder:\n%s", got)
	}
}

func TestManifestCreateExcludesLocalOverlays(t *testing.T) {
	entries := initLibraryEntries()
	entries["deploy-host/skeleton/overlays/local/kustomization.yaml.tmpl"] = "resources: []\n"
	entries["deploy-component/skeleton/overlays/local/kustomization.yaml.tmpl"] = "resources: []\n"
	f := newInitFixtureWith(t, entries)

	if _, _, err := runInit(t, f.options(t, nil, nil)); err != nil {
		t.Fatalf("manifest create: %v", err)
	}

	work := f.clone(t, "manifests-create/sales-distribution-all")
	base := filepath.Join(work, "domains", "sales", "distribution")
	for _, unit := range []string{"host", "extractor"} {
		local := filepath.Join(base, unit, "overlays", localEnv)
		if _, err := os.Stat(local); !os.IsNotExist(err) {
			t.Errorf("%s = %v, want no local overlay in GitOps manifests", local, err)
		}
	}
}
