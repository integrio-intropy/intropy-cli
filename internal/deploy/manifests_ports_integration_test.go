//go:build integration

package deploy

import (
	"strings"
	"testing"
)

func TestManifestCreateKeepsPlaceholdersAndNotesPendingPorts(t *testing.T) {
	f := newInitFixture(t)

	_, stderr, err := runInit(t, f.options(nil, nil))
	if err != nil {
		t.Fatalf("manifest create: %v", err)
	}
	if !strings.Contains(stderr, "note: port erp has no binding for dev; its manifests keep the REPLACE-ME scaffold") {
		t.Errorf("stderr should note the pending port:\n%q", stderr)
	}
}

func TestManifestCreateRendersThePlaceholderForAnUnboundPort(t *testing.T) {
	entries := initLibraryEntries()
	entries["deploy-host/skeleton/base/bindings/bindings.yaml.tmpl"] = `{{- range .topology.ports }}
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
		t.Errorf("an unbound port should keep the placeholder:\n%s", got)
	}
}
