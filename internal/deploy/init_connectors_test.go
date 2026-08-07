//go:build integration

package deploy

import (
	"os"
	"strings"
	"testing"
)

// withBindingsCatalog adds the GitOps binding catalog to the fixture
// deploy-component template, as a library release that ships one does.
func withBindingsCatalog(entries map[string]string, catalog string) map[string]string {
	out := make(map[string]string, len(entries))
	for k, v := range entries {
		out[k] = v
	}
	tmpl := out["deploy-component/template.yaml"]
	marker := "spec:\n"
	if !strings.Contains(tmpl, marker) {
		panic("fixture deploy-component template lost its spec: line")
	}
	out["deploy-component/template.yaml"] = strings.Replace(tmpl, marker, marker+"  bindings: ["+catalog+"]\n", 1)
	return out
}

// Interactive GitOps init asks once per connector per environment, offers the
// previous environment's answer first, and records everything in
// deploy-values.yaml.
func TestInitPromptsForConnectorBindingsAndRecordsThem(t *testing.T) {
	f := newInitFixtureWith(t, withBindingsCatalog(initLibraryEntries(), "sftp, http"))

	opts := f.options(nil, nil)
	opts.NoInput = false
	// Two connectors × three environments, every answer the menu default (the
	// first option).
	opts.Stdin = strings.NewReader("1\n1\n1\n1\n1\n1\n")
	_, stderr, err := runInit(t, opts)
	if err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "which binding for dev?") {
		t.Errorf("the prompt did not name the environment:\n%s", stderr)
	}
	vals, err := loadDeployValues(deployValuesPath(opts.SourceDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, conn := range []string{"erp", "price-master"} {
		for _, env := range []string{"dev", "staging", "prod"} {
			if vals.Connectors[conn][env] != "sftp" {
				t.Errorf("deploy-values.yaml[%s][%s] = %q, want the recorded default", conn, env, vals.Connectors[conn][env])
			}
		}
	}
}

// Under --no-input a GitOps run binds nothing and keeps the placeholder
// scaffold, naming each pending connector on stderr — the CI contract.
func TestInitNoInputKeepsPlaceholdersAndNotesPendingConnectors(t *testing.T) {
	f := newInitFixtureWith(t, withBindingsCatalog(initLibraryEntries(), "sftp, http"))

	_, stderr, err := runInit(t, f.options(nil, nil))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(stderr, "note: connector erp has no binding for dev; its manifests keep the REPLACE-ME scaffold") {
		t.Errorf("stderr should note the pending connector:\n%q", stderr)
	}
	if _, err := os.Stat(deployValuesPath(f.sourceDir)); !os.IsNotExist(err) {
		t.Errorf("no answers given, so no state file should exist: %v", err)
	}
}

// A library older than spec.bindings offers no menu: recorded answers still
// validate, and there is nothing to prompt with.
func TestInitToleratesALibraryWithoutABindingsCatalog(t *testing.T) {
	f := newInitFixture(t) // the base fixture declares no spec.bindings

	_, stderr, err := runInit(t, f.options(nil, nil))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(stderr, "has no binding for") {
		t.Errorf("stderr should note the pending connectors:\n%s", stderr)
	}
}

// A recorded GitOps binding outside the catalog is an error naming the file,
// the value and the catalog — the same contract the local env has.
func TestInitRejectsARecordedGitOpsBindingOutsideTheCatalog(t *testing.T) {
	f := newInitFixtureWith(t, withBindingsCatalog(initLibraryEntries(), "sftp, http"))
	writeDeployValues(t, f.sourceDir, "connectors:\n  erp:\n    dev: pigeon\n")

	opts := f.options(nil, nil)
	opts.Stdin = strings.NewReader("")
	if _, _, err := runInit(t, opts); err == nil {
		t.Fatal("expected a catalog validation error")
	} else {
		for _, want := range []string{"erp", "pigeon", "dev", "sftp, http"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should name %q: %v", want, err)
			}
		}
	}
}

// A recorded binding travels on the topology model: the host base renders the
// environment's answer as the Dapr spec.type, and environments with different
// answers may differ under base/bindings/ without tripping the merge guard.
func TestInitRendersTheRecordedBindingPerEnvironment(t *testing.T) {
	entries := withBindingsCatalog(initLibraryEntries(), "sftp, http")
	entries["deploy-host/skeleton/base/bindings/bindings.yaml.tmpl"] = `{{- range .topology.connectors }}
---
kind: Component
metadata:
  name: {{ .name }}
spec:
  {{- $b := dig "binding" "" . }}
  type: {{ if $b }}bindings.{{ $b }}{{ else }}REPLACE-ME-BINDING-TYPE{{ end }}
{{- end }}
`
	entries["deploy-host/skeleton/base/kustomization.yaml.tmpl"] = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - dapr/pubsub-rabbitmq.yaml
  - bindings/bindings.yaml
`
	f := newInitFixtureWith(t, entries)
	writeDeployValues(t, f.sourceDir, "connectors:\n  erp:\n    dev: sftp\n    staging: sftp\n    prod: http\n  price-master:\n    dev: sftp\n    staging: sftp\n    prod: sftp\n")

	if _, stderr, err := runInit(t, f.options(nil, nil)); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr)
	}

	got := readTreeFile(t, f.clone(t, "deploy-init/sales-distribution"),
		"domains/sales/distribution/host/base/bindings/bindings.yaml")
	if !strings.Contains(got, "type: bindings.http") {
		t.Errorf("the last environment's binding did not render:\n%s", got)
	}
	if strings.Contains(got, "REPLACE-ME-BINDING-TYPE") {
		t.Errorf("a bound connector kept the placeholder:\n%s", got)
	}
}

// An unbound connector renders the placeholder scaffold exactly as before the
// question existed.
func TestInitRendersThePlaceholderForAnUnboundConnector(t *testing.T) {
	entries := withBindingsCatalog(initLibraryEntries(), "sftp, http")
	entries["deploy-host/skeleton/base/bindings/bindings.yaml.tmpl"] = `{{- range .topology.connectors }}
---
kind: Component
metadata:
  name: {{ .name }}
spec:
  {{- $b := dig "binding" "" . }}
  type: {{ if $b }}bindings.{{ $b }}{{ else }}REPLACE-ME-BINDING-TYPE{{ end }}
{{- end }}
`
	f := newInitFixtureWith(t, entries)

	if _, _, err := runInit(t, f.options(nil, nil)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got := readTreeFile(t, f.clone(t, "deploy-init/sales-distribution"),
		"domains/sales/distribution/host/base/bindings/bindings.yaml")
	if !strings.Contains(got, "type: REPLACE-ME-BINDING-TYPE") {
		t.Errorf("an unbound connector should keep the placeholder:\n%s", got)
	}
}
