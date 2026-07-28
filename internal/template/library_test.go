package template

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// newLibraryServer serves a two-template library and counts tarball fetches, so
// a test can prove one download covers both templates.
func newLibraryServer(t *testing.T, tag string, fetches *atomic.Int32) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", map[string]string{
		"deploy-host/template.yaml":                     libraryHostYAML,
		"deploy-host/skeleton/base/kustomization.yaml":  "resources: []\n",
		"deploy-component/template.yaml":                libraryComponentYAML,
		"deploy-component/skeleton/component.yaml.tmpl": "name: {{ .name }}\n",
		"no-skeleton/template.yaml":                     libraryHostYAML,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
		if fetches != nil {
			fetches.Add(1)
		}
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux)
}

func fetchTestLibrary(t *testing.T, srv *httptest.Server, version string) *Library {
	t.Helper()
	lib, err := FetchLibrary(context.Background(), LibraryOptions{
		Version:       version,
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
		HTTP:          srv.Client(),
	})
	if err != nil {
		t.Fatalf("FetchLibrary: %v", err)
	}
	t.Cleanup(lib.Close)
	return lib
}

// One download for every template: a component's manifests and its system's
// shared manifests drifting apart by a release would be a subtle, ugly bug.
func TestLibraryFetchesOnceForSeveralTemplates(t *testing.T) {
	var fetches atomic.Int32
	srv := newLibraryServer(t, "v1.0.0", &fetches)
	defer srv.Close()

	lib := fetchTestLibrary(t, srv, "")
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
	if got := fetches.Load(); got != 1 {
		t.Errorf("tarball fetched %d times, want 1", got)
	}
}

func TestLibraryRefDescribesTheFetch(t *testing.T) {
	srv := newLibraryServer(t, "v2.3.4", nil)
	defer srv.Close()

	if got := fetchTestLibrary(t, srv, "v2.3.4").Ref(); got != "o/r@v2.3.4" {
		t.Errorf("Ref() = %q", got)
	}
}

func TestLibraryOpenMissingTemplate(t *testing.T) {
	srv := newLibraryServer(t, "v1.0.0", nil)
	defer srv.Close()

	_, _, err := fetchTestLibrary(t, srv, "v1.0.0").Open("deploy-nonexistent")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "o/r@v1.0.0") {
		t.Errorf("error should name the library version: %v", err)
	}
}

func TestLibraryOpenTemplateWithoutSkeleton(t *testing.T) {
	srv := newLibraryServer(t, "v1.0.0", nil)
	defer srv.Close()

	_, _, err := fetchTestLibrary(t, srv, "v1.0.0").Open("no-skeleton")
	if err == nil || !strings.Contains(err.Error(), "skeleton") {
		t.Fatalf("expected a missing-skeleton error, got %v", err)
	}
}

// The name becomes a path inside the extracted tarball, so it stays validated.
func TestLibraryOpenRejectsTraversal(t *testing.T) {
	srv := newLibraryServer(t, "v1.0.0", nil)
	defer srv.Close()
	lib := fetchTestLibrary(t, srv, "v1.0.0")

	for _, name := range []string{"", "..", "../etc", "a/b", ".hidden"} {
		if _, _, err := lib.Open(name); err == nil {
			t.Errorf("Open(%q) should be rejected", name)
		}
	}
}

func TestFetchLibraryAnnouncesTheVersion(t *testing.T) {
	srv := newLibraryServer(t, "v1.2.3", nil)
	defer srv.Close()

	var stderr bytes.Buffer
	lib, err := FetchLibrary(context.Background(), LibraryOptions{
		Owner: "o", Repo: "r", GitHubBaseURL: srv.URL, HTTP: srv.Client(), Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	if !strings.Contains(stderr.String(), "o/r@v1.2.3") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDefaultLibraryIsTheOfficialOne(t *testing.T) {
	owner, repo := DefaultLibrary()
	if owner != defaultTemplateOwner || repo != defaultTemplateRepo {
		t.Errorf("DefaultLibrary() = %s/%s", owner, repo)
	}
}
