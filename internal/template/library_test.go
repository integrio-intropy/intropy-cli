package template

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const libraryHostYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-host
spec:
  parameters:
    type: object
    properties:
      pubsub: { type: string }
`

const libraryComponentYAML = `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-component
spec:
  parameters:
    type: object
    properties:
      name: { type: string }
`

// newLibrary builds a two-template library so a test can prove one fetch
// covers both templates.
func newLibrary(t *testing.T, tag string) *testLibrary {
	t.Helper()
	return newTestLibrary(t, tag, map[string]string{
		"deploy-host/template.yaml":                     libraryHostYAML,
		"deploy-host/skeleton/base/kustomization.yaml":  "resources: []\n",
		"deploy-component/template.yaml":                libraryComponentYAML,
		"deploy-component/skeleton/component.yaml.tmpl": "name: {{ .name }}\n",
		"no-skeleton/template.yaml":                     libraryHostYAML,
	})
}

func fetchTestLibrary(t *testing.T, lib *testLibrary, version, cacheRoot string) *Library {
	t.Helper()
	opts := lib.sourceOpts(cacheRoot, nil)
	got, err := FetchLibrary(context.Background(), LibraryOptions{
		Version: version,
		Source:  opts,
	})
	if err != nil {
		t.Fatalf("FetchLibrary: %v", err)
	}
	t.Cleanup(got.Close)
	return got
}

// One fetch for every template: a component's manifests and its system's
// shared manifests drifting apart by a release would be a subtle, ugly bug.
func TestLibraryFetchesOnceForSeveralTemplates(t *testing.T) {
	repo := newLibrary(t, "v1.0.0")

	lib := fetchTestLibrary(t, repo, "", t.TempDir())
	if lib.Version != "v1.0.0" {
		t.Errorf("Version = %q, want the resolved latest release", lib.Version)
	}

	for _, name := range []string{"deploy-host", "deploy-component"} {
		tmpl, skeleton, err := lib.Open(name)
		if err != nil {
			t.Fatalf("Open(%q): %v", name, err)
		}
		if tmpl.Metadata.Name != name {
			t.Errorf("Open(%q) loaded %q", name, tmpl.Metadata.Name)
		}
		if filepath.Base(skeleton) != "skeleton" {
			t.Errorf("skeleton dir = %q", skeleton)
		}
	}
}

func TestLibraryRefDescribesTheFetch(t *testing.T) {
	repo := newLibrary(t, "v2.3.4")

	want := defaultTemplateOwner + "/" + defaultTemplateRepo + "@v2.3.4"
	if got := fetchTestLibrary(t, repo, "v2.3.4", t.TempDir()).Ref(); got != want {
		t.Errorf("Ref() = %q, want %q", got, want)
	}
}

func TestLibraryOpenMissingTemplate(t *testing.T) {
	repo := newLibrary(t, "v1.0.0")

	_, _, err := fetchTestLibrary(t, repo, "v1.0.0", t.TempDir()).Open("deploy-nonexistent")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "@"+"v1.0.0") {
		t.Errorf("error should name the library version: %v", err)
	}
}

func TestLibraryOpenTemplateWithoutSkeleton(t *testing.T) {
	repo := newLibrary(t, "v1.0.0")

	_, _, err := fetchTestLibrary(t, repo, "v1.0.0", t.TempDir()).Open("no-skeleton")
	if err == nil || !strings.Contains(err.Error(), "skeleton") {
		t.Fatalf("expected a missing-skeleton error, got %v", err)
	}
}

// The name becomes a path inside the checkout, so it stays validated.
func TestLibraryOpenRejectsTraversal(t *testing.T) {
	repo := newLibrary(t, "v1.0.0")
	lib := fetchTestLibrary(t, repo, "v1.0.0", t.TempDir())

	for _, name := range []string{"", "..", "../etc", "a/b", ".hidden"} {
		if _, _, err := lib.Open(name); err == nil {
			t.Errorf("Open(%q) should be rejected", name)
		}
	}
}

func TestFetchLibraryAnnouncesTheVersion(t *testing.T) {
	repo := newLibrary(t, "v1.2.3")

	var stderr bytes.Buffer
	opts := repo.sourceOpts(t.TempDir(), nil)
	opts.Stderr = &stderr
	lib, err := FetchLibrary(context.Background(), LibraryOptions{Source: opts, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	if !strings.Contains(stderr.String(), "@v1.2.3") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDefaultLibraryIsTheOfficialOne(t *testing.T) {
	owner, repo := DefaultLibrary()
	if owner != defaultTemplateOwner || repo != defaultTemplateRepo {
		t.Errorf("DefaultLibrary() = %s/%s", owner, repo)
	}
}
