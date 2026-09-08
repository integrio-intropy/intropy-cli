package template

import (
	"context"
	"testing"
)

// The listing reads the cached checkout, not the GitHub contents API: file
// entries are excluded by the layout (templates are directories), hidden
// directories stay hidden, and names come back sorted.
func TestList(t *testing.T) {
	lib := newTestLibrary(t, "v1.2.3", map[string]string{
		"transactional/template.yaml":           testTemplateYAML,
		"transactional/skeleton/README.md.tmpl": "x\n",
		"hello-world/template.yaml":             testTemplateYAML,
		"hello-world/skeleton/README.md.tmpl":   "x\n",
		"README.md":                             "not a template\n",
	})

	got, err := List(context.Background(), ListOptions{
		Source: lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
	want := []string{"hello-world", "transactional"}
	if len(got.Templates) != len(want) {
		t.Fatalf("Templates = %v, want %v", got.Templates, want)
	}
	for i, name := range want {
		if got.Templates[i] != name {
			t.Errorf("Templates[%d] = %q, want %q", i, got.Templates[i], name)
		}
	}
}

// A pinned version never asks the API which release is latest.
func TestListPinnedVersionSkipsReleaseLookup(t *testing.T) {
	lib := newTestLibrary(t, "v1", map[string]string{
		"hello-world/template.yaml":           testTemplateYAML,
		"hello-world/skeleton/README.md.tmpl": "x\n",
	})
	lib.failLatest()

	got, err := List(context.Background(), ListOptions{
		Version: "v1",
		Source:  lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Version != "v1" {
		t.Errorf("Version = %q, want v1", got.Version)
	}
	if len(got.Templates) != 1 || got.Templates[0] != "hello-world" {
		t.Errorf("Templates = %v", got.Templates)
	}
}
