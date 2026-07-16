package template

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRender(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "README.md.tmpl"), "Hello {{ .Name | upper }}\n")
	writeFile(t, filepath.Join(src, "plain.txt"), "literal {{not-a-template}}\n")
	writeFile(t, filepath.Join(src, "nested", "data.bin"), "\x00\x01\x02\x03")

	if err := Render(src, dst, map[string]any{"Name": "intropy"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// .tmpl is rendered with suffix stripped
	gotReadme := readFile(t, filepath.Join(dst, "README.md"))
	if gotReadme != "Hello INTROPY\n" {
		t.Errorf("README = %q", gotReadme)
	}
	// non-tmpl files pass through untouched
	if got := readFile(t, filepath.Join(dst, "plain.txt")); got != "literal {{not-a-template}}\n" {
		t.Errorf("plain.txt = %q", got)
	}
	// binary survives byte-for-byte
	got, err := os.ReadFile(filepath.Join(dst, "nested", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0, 1, 2, 3}) {
		t.Errorf("binary mismatch: %v", got)
	}
}

func TestRenderTemplatedPaths(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// path-only templating, contents copied verbatim
	writeFile(t, filepath.Join(src, "{{ .Name }}.http"), "GET /\n")
	// path and contents both rendered
	writeFile(t, filepath.Join(src, "{{ .Name | lower }}.go.tmpl"), "package {{ .Name | lower }}\n")
	// templated directory segment
	writeFile(t, filepath.Join(src, "cmd", "{{ .Name }}", "main.go"), "package main\n")

	if err := Render(src, dst, map[string]any{"Name": "Orders"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := readFile(t, filepath.Join(dst, "Orders.http")); got != "GET /\n" {
		t.Errorf("Orders.http = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "orders.go")); got != "package orders\n" {
		t.Errorf("orders.go = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "cmd", "Orders", "main.go")); got != "package main\n" {
		t.Errorf("cmd/Orders/main.go = %q", got)
	}
}

func TestRenderMissingKeyInPathErrors(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "{{ .Undefined }}.txt"), "x")
	if err := Render(src, dst, map[string]any{}); err == nil {
		t.Fatal("expected error for missing key in path")
	}
}

func TestRenderMissingKeyErrors(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "a.tmpl"), "{{ .Undefined }}")
	if err := Render(src, dst, map[string]any{}); err == nil {
		t.Fatal("expected error for missing key")
	}
	if _, err := os.Stat(filepath.Join(dst, "a")); !os.IsNotExist(err) {
		t.Fatalf("failed render should not leave partial output file, stat err=%v", err)
	}
}

func TestRenderMissingKeyPreservesExistingFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "a.tmpl"), "replacement {{ .Undefined }}")
	writeFile(t, filepath.Join(dst, "a"), "original")

	if err := Render(src, dst, map[string]any{}); err == nil {
		t.Fatal("expected error for missing key")
	}
	if got := readFile(t, filepath.Join(dst, "a")); got != "original" {
		t.Fatalf("failed render should preserve existing file, got %q", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
