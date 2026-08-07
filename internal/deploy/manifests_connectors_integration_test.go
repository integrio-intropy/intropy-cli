//go:build integration

package deploy

import (
	"strings"
	"testing"
)

func TestManifestCreateKeepsPlaceholdersAndNotesPendingConnectors(t *testing.T) {
	f := newInitFixture(t)

	_, stderr, err := runInit(t, f.options(nil, nil))
	if err != nil {
		t.Fatalf("manifest create: %v", err)
	}
	if !strings.Contains(stderr, "note: connector erp has no binding for dev; its manifests keep the REPLACE-ME scaffold") {
		t.Errorf("stderr should note the pending connector:\n%q", stderr)
	}
}

func TestManifestCreateRendersThePlaceholderForAnUnboundConnector(t *testing.T) {
	entries := initLibraryEntries()
	entries["deploy-host/skeleton/base/bindings/bindings.yaml.tmpl"] = `{{- range .topology.connectors }}
---
kind: Component
metadata:
  name: {{ .name }}
spec:
  type: REPLACE-ME-BINDING-TYPE
{{- end }}
`
	f := newInitFixtureWith(t, entries)

	if _, _, err := runInit(t, f.options(nil, nil)); err != nil {
		t.Fatalf("manifest create: %v", err)
	}

	got := readTreeFile(t, f.clone(t, "manifests-create/sales-distribution-all"),
		"domains/sales/distribution/host/base/bindings/bindings.yaml")
	if !strings.Contains(got, "type: REPLACE-ME-BINDING-TYPE") {
		t.Errorf("an unbound connector should keep the placeholder:\n%s", got)
	}
}
