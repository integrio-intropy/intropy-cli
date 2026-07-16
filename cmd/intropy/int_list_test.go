package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScaffoldT(t *testing.T, dir, template, version string) {
	t.Helper()
	intropyDir := filepath.Join(dir, ".intropy")
	if err := os.MkdirAll(intropyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(intropyDir, "scaffold.json"),
		`{"schemaVersion":1,"template":"`+template+`","owner":"integrio-intropy","repo":"intropy-templates","version":"`+version+`","values":{"appId":"int1"}}`+"\n")
}

func TestIntListEmpty(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stderr.String(), "No scaffolded integrations found.") {
		t.Errorf("expected empty notice on stderr, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
}

func TestIntListTable(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffoldT(t, filepath.Join(tmp, "orders"), "hello-world", "v0.1.6")
	writeScaffoldT(t, filepath.Join(tmp, "team", "invoices"), "transactional", "v0.2.0")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"PATH", "TEMPLATE", "VERSION", "orders", "hello-world", "v0.1.6", filepath.Join("team", "invoices"), "transactional", "v0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
}

func TestIntListJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffoldT(t, filepath.Join(tmp, "orders"), "hello-world", "v0.1.6")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "list", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e["path"] != "orders" || e["template"] != "hello-world" || e["version"] != "v0.1.6" {
		t.Errorf("unexpected entry: %v", e)
	}
	if _, ok := e["values"]; !ok {
		t.Errorf("entry missing scaffold values: %v", e)
	}
}

func TestIntListJSONEmptyIsArray(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "list", "--output", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "[]" {
		t.Errorf("stdout = %q, want []", got)
	}
}

func TestIntListRejectsMissingDir(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"int", "list", "does-not-exist"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for missing directory")
	}
}
