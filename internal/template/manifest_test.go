package template

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, templateManifestName)
	body := `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-template
  title: Test
  description: For tests
  tags: [test]
spec:
  parameters:
    type: object
    required: [integrationName]
    properties:
      integrationName:
        type: string
        title: Integration Name
        description: Name of the integration
      namespace:
        type: string
        default: default
  values:
    fullName: "{{ .integrationName }}-{{ .namespace }}"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplate(path)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	if tmpl.Metadata.Name != "test-template" {
		t.Errorf("Name = %q, want test-template", tmpl.Metadata.Name)
	}
	fields := tmpl.Fields()
	if len(fields) != 2 {
		t.Fatalf("Fields = %d, want 2", len(fields))
	}
	if fields[0].Name != "integrationName" || !fields[0].Required {
		t.Errorf("field 0 = %+v, want required integrationName", fields[0])
	}
	if fields[1].Name != "namespace" || fields[1].Default != "default" {
		t.Errorf("field 1 = %+v, want namespace with default", fields[1])
	}
	if tmpl.Spec.Values["fullName"] == "" {
		t.Error("spec.values not loaded")
	}
}

func TestLoadTemplateRejectsUnknownAPIVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, templateManifestName)
	body := `
apiVersion: scaffolder.backstage.io/v1beta3
kind: Template
metadata:
  name: x
spec:
  parameters:
    type: object
    properties: {}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTemplate(path); err == nil {
		t.Fatal("expected error for unsupported apiVersion")
	}
}

func TestLoadTemplateRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, templateManifestName)
	body := `
apiVersion: intropy.dev/v1
kind: Widget
metadata:
  name: x
spec:
  parameters:
    type: object
    properties: {}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTemplate(path); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestLoadTemplateRequiresName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, templateManifestName)
	body := `
apiVersion: intropy.dev/v1
kind: Template
metadata: {}
spec:
  parameters:
    type: object
    properties: {}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTemplate(path); err == nil {
		t.Fatal("expected error for missing metadata.name")
	}
}

func TestLoadTemplateMissing(t *testing.T) {
	if _, err := LoadTemplate(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFieldsPreservesYAMLOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, templateManifestName)
	body := `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: order-test
spec:
  parameters:
    type: object
    properties:
      zeta: { type: string }
      alpha: { type: string }
      mu: { type: string }
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplate(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zeta", "alpha", "mu"}
	fields := tmpl.Fields()
	if len(fields) != len(want) {
		t.Fatalf("Fields = %d, want %d", len(fields), len(want))
	}
	for i, name := range want {
		if fields[i].Name != name {
			t.Errorf("Fields[%d] = %q, want %q", i, fields[i].Name, name)
		}
	}
}
