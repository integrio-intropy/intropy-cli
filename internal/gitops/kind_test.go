package gitops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func writeComponentYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	gittest.WriteFile(t, filepath.Join(dir, ComponentFileName), content)
	return dir
}

// An absent kind means service, so every component.yaml predating the field is
// unchanged in meaning.
func TestComponentKindDefaultsToService(t *testing.T) {
	dir := writeComponentYAML(t, validComponentYAML)
	comp, err := LoadComponentConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if comp.Kind != "" {
		t.Errorf("Kind = %q, want empty", comp.Kind)
	}
	if comp.IsShared() {
		t.Error("a component without a kind must not be shared")
	}
}

func TestComponentKindShared(t *testing.T) {
	dir := writeComponentYAML(t, `schemaVersion: 1
kind: shared
name: host
environments: [dev, staging, prod]
`)
	comp, err := LoadComponentConfig(dir)
	if err != nil {
		t.Fatalf("a shared component needs no images: %v", err)
	}
	if !comp.IsShared() {
		t.Error("IsShared() = false")
	}
	if len(comp.Images) != 0 {
		t.Errorf("Images = %v", comp.Images)
	}
}

func TestComponentKindServiceStillRequiresImages(t *testing.T) {
	dir := writeComponentYAML(t, `schemaVersion: 1
kind: service
name: order-extractor
environments: [dev]
`)
	_, err := LoadComponentConfig(dir)
	if err == nil {
		t.Fatal("expected an error: a service has nothing to pin without images")
	}
	if !strings.Contains(err.Error(), "images") {
		t.Errorf("error should mention images: %v", err)
	}
}

// kustomize edit set image adds an entry matching nothing rather than failing,
// so a shared component listing images would look pinnable and never be pinned.
func TestComponentKindSharedRejectsImages(t *testing.T) {
	dir := writeComponentYAML(t, `schemaVersion: 1
kind: shared
name: host
images:
  - name: harbor.intropy.io/integrations/host
environments: [dev]
`)
	_, err := LoadComponentConfig(dir)
	if err == nil {
		t.Fatal("expected an error for images on a shared component")
	}
	if !strings.Contains(err.Error(), "images") {
		t.Errorf("error should mention images: %v", err)
	}
}

func TestComponentKindRejectsUnknownValue(t *testing.T) {
	dir := writeComponentYAML(t, `schemaVersion: 1
kind: daemon
name: order-extractor
images:
  - name: harbor.intropy.io/integrations/order-extractor
environments: [dev]
`)
	_, err := LoadComponentConfig(dir)
	if err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
	if !strings.Contains(err.Error(), "daemon") {
		t.Errorf("error should quote the offending value: %v", err)
	}
}
