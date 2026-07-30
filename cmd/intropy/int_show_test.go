package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntShowPlain(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "orders")
	writeScaffoldT(t, project, "hello-world", "v0.1.6")
	// Run from a subdirectory: show must walk up to the project root.
	nested := filepath.Join(project, "src", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "show"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{project, "hello-world", "integrio-intropy/intropy-templates@v0.1.6", "appId: int1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestIntShowExplicitDir(t *testing.T) {
	tmp := t.TempDir()
	writeScaffoldT(t, filepath.Join(tmp, "orders"), "hello-world", "v0.1.6")
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "show", filepath.Join(tmp, "orders")})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "orders") {
		t.Errorf("expected project path in output, got:\n%s", stdout.String())
	}
}

func TestIntShowJSON(t *testing.T) {
	tmp := t.TempDir()
	writeScaffoldT(t, filepath.Join(tmp, "orders"), "hello-world", "v0.1.6")
	t.Chdir(filepath.Join(tmp, "orders"))

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "show", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if doc["template"] != "hello-world" || doc["version"] != "v0.1.6" {
		t.Errorf("unexpected document: %v", doc)
	}
	if _, ok := doc["values"]; !ok {
		t.Errorf("document missing values: %v", doc)
	}
}

func TestIntShowNotFound(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "show"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error outside a scaffolded integration")
	}
	if !strings.Contains(err.Error(), "no integration found") {
		t.Errorf("expected 'no integration found', got: %v", err)
	}
	if !strings.Contains(err.Error(), "intropy int list") {
		t.Errorf("expected a pointer to int list, got: %v", err)
	}
}

func TestIntDescribeStubPointsAtReplacements(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "describe", "hello-world"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected the describe stub to fail")
	}
	if !strings.Contains(err.Error(), "template show hello-world") {
		t.Errorf("expected a pointer to template show, got: %v", err)
	}
	if !strings.Contains(err.Error(), "intropy int show") {
		t.Errorf("expected a pointer to int show, got: %v", err)
	}
}
