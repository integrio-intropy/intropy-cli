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

func outcomeByPath(outcomes []FileOutcome) map[string]FileOutcomeKind {
	m := make(map[string]FileOutcomeKind, len(outcomes))
	for _, o := range outcomes {
		m[o.Path] = o.Outcome
	}
	return m
}

func TestRenderUpdateClassifiesFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "same.txt.tmpl"), "value: {{ .v }}\n")
	writeFile(t, filepath.Join(src, "new.txt"), "fresh\n")
	writeFile(t, filepath.Join(src, "changed.txt"), "updated\n")

	writeFile(t, filepath.Join(dst, "same.txt"), "value: 1\n")
	writeFile(t, filepath.Join(dst, "changed.txt"), "original\n")

	outcomes, err := RenderUpdate(src, dst, map[string]any{"v": 1}, nil, RenderUpdateOptions{})
	if err != nil {
		t.Fatalf("RenderUpdate: %v", err)
	}
	got := outcomeByPath(outcomes)
	want := map[string]FileOutcomeKind{
		"same.txt":    OutcomeUnchanged,
		"new.txt":     OutcomeCreated,
		"changed.txt": OutcomeConflict,
	}
	for path, wantKind := range want {
		if got[path] != wantKind {
			t.Errorf("%s: outcome = %q, want %q", path, got[path], wantKind)
		}
	}

	// unchanged and conflict leave the destination exactly as it was.
	if got := readFile(t, filepath.Join(dst, "same.txt")); got != "value: 1\n" {
		t.Errorf("same.txt rewritten: %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "changed.txt")); got != "original\n" {
		t.Errorf("conflict overwrote changed.txt: %q", got)
	}
	// the created file did land.
	if got := readFile(t, filepath.Join(dst, "new.txt")); got != "fresh\n" {
		t.Errorf("new.txt = %q", got)
	}
}

func TestRenderUpdateForceOverwritesConflict(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "changed.txt"), "updated\n")
	writeFile(t, filepath.Join(dst, "changed.txt"), "original\n")

	outcomes, err := RenderUpdate(src, dst, nil, nil, RenderUpdateOptions{Force: true})
	if err != nil {
		t.Fatalf("RenderUpdate: %v", err)
	}
	if got := outcomeByPath(outcomes)["changed.txt"]; got != OutcomeUpdated {
		t.Errorf("outcome = %q, want %q", got, OutcomeUpdated)
	}
	if got := readFile(t, filepath.Join(dst, "changed.txt")); got != "updated\n" {
		t.Errorf("changed.txt = %q", got)
	}
}

func TestRenderUpdateDryRunWritesNothing(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "new.txt"), "fresh\n")
	writeFile(t, filepath.Join(src, "nested", "deep.txt.tmpl"), "v {{ .v }}\n")
	writeFile(t, filepath.Join(src, "changed.txt"), "updated\n")
	writeFile(t, filepath.Join(dst, "changed.txt"), "original\n")

	outcomes, err := RenderUpdate(src, dst, map[string]any{"v": 1}, nil, RenderUpdateOptions{DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("RenderUpdate: %v", err)
	}
	got := outcomeByPath(outcomes)
	if got["new.txt"] != OutcomeCreated || got["nested/deep.txt"] != OutcomeCreated {
		t.Errorf("dry-run outcomes for new files: %v", got)
	}
	if got["changed.txt"] != OutcomeUpdated {
		t.Errorf("dry-run with force: outcome = %q, want %q", got["changed.txt"], OutcomeUpdated)
	}

	// The destination tree is untouched: no new files, no modified ones.
	if _, err := os.Stat(filepath.Join(dst, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote new.txt")
	}
	if _, err := os.Stat(filepath.Join(dst, "nested")); !os.IsNotExist(err) {
		t.Errorf("dry-run created the nested directory")
	}
	if got := readFile(t, filepath.Join(dst, "changed.txt")); got != "original\n" {
		t.Errorf("dry-run modified changed.txt: %q", got)
	}
}

func TestRenderUpdateHonorsFileRules(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "Topics.cs.tmpl"), "topics {{ len .topics }}\n")
	writeFile(t, filepath.Join(src, "always.txt"), "always\n")

	rules := []FileRule{{Path: "Topics.cs.tmpl", When: "{{ gt (len .topics) 0 }}"}}
	outcomes, err := RenderUpdate(src, dst, map[string]any{"topics": []any{}}, rules, RenderUpdateOptions{})
	if err != nil {
		t.Fatalf("RenderUpdate: %v", err)
	}
	got := outcomeByPath(outcomes)
	if _, ok := got["Topics.cs"]; ok {
		t.Errorf("excluded file was rendered: %v", got)
	}
	if got["always.txt"] != OutcomeCreated {
		t.Errorf("always.txt outcome = %q", got["always.txt"])
	}
}
