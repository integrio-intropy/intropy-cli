package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProject creates dir with a scaffold.json naming the template after the
// last path segment, so tests can assert which projects were found.
func writeProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := testScaffold()
	s.Template = filepath.Base(dir)
	if err := WriteScaffold(dir, s); err != nil {
		t.Fatal(err)
	}
}

func entryPaths(entries []ScaffoldEntry) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths
}

func TestListScaffoldsFindsNestedProjects(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "alpha"))
	writeProject(t, filepath.Join(root, "team", "beta"))

	entries, warnings := ListScaffolds(root)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	want := []string{filepath.Join(root, "alpha"), filepath.Join(root, "team", "beta")}
	if got := entryPaths(entries); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("paths = %v, want %v", got, want)
	}
	if entries[0].Template != "alpha" || entries[1].Template != "beta" {
		t.Errorf("templates = %q, %q", entries[0].Template, entries[1].Template)
	}
}

func TestListScaffoldsSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", ".intropy", "node_modules", "bin", "dist"} {
		writeProject(t, filepath.Join(root, dir, "hidden"))
	}
	writeProject(t, filepath.Join(root, "visible"))

	entries, warnings := ListScaffolds(root)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got := entryPaths(entries); len(got) != 1 || got[0] != filepath.Join(root, "visible") {
		t.Errorf("paths = %v, want only visible", got)
	}
}

func TestListScaffoldsDoesNotDescendIntoMatchedProject(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "outer"))
	writeProject(t, filepath.Join(root, "outer", "inner"))

	entries, warnings := ListScaffolds(root)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got := entryPaths(entries); len(got) != 1 || got[0] != filepath.Join(root, "outer") {
		t.Errorf("paths = %v, want only outer", got)
	}
}

func TestListScaffoldsMatchesRootItself(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root)
	writeProject(t, filepath.Join(root, "nested"))

	entries, warnings := ListScaffolds(root)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got := entryPaths(entries); len(got) != 1 || got[0] != root {
		t.Errorf("paths = %v, want only root", got)
	}
}

func TestListScaffoldsWarnsOnMalformedRecord(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "broken", ".intropy")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "scaffold.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProject(t, filepath.Join(root, "ok"))

	entries, warnings := ListScaffolds(root)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if got := entryPaths(entries); len(got) != 1 || got[0] != filepath.Join(root, "ok") {
		t.Errorf("paths = %v, want only ok", got)
	}
}
