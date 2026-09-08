package template

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// recordingRunner captures the argv git would have been given and delegates
// to the real binary — clone behaviour under test is git's own.
type recordingRunner struct {
	base  command.Runner
	calls [][]string
}

func (r *recordingRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.base.Run(ctx, dir, name, args...)
}

// cloneCount reports how many git clone invocations ran.
func (r *recordingRunner) cloneCount() int {
	n := 0
	for _, call := range r.calls {
		if call[0] != "git" {
			continue
		}
		for _, a := range call[1:] {
			if a == "clone" {
				n++
			}
		}
	}
	return n
}

// testLibrary builds a real git repository holding one release tag of a
// template library and returns the fetch seams pointed at it: a file://
// remote (plain paths ignore --depth), a private cache root, and an API stub
// for latest-release resolution.
//
// Real git rather than a tarball server: clone is exactly the operation where
// a mock encodes our assumptions instead of git's behaviour, and it is what
// production runs.
type testLibrary struct {
	repo   string
	server *httptest.Server
}

// newTestLibrary creates a library whose files map is committed on main and
// tagged with tag. Paths are repo-relative ("extractor/template.yaml").
func newTestLibrary(t *testing.T, tag string, files map[string]string) *testLibrary {
	t.Helper()
	repo := gittest.NewRepo(t, "main")
	for path, content := range files {
		gittest.WriteFile(t, filepath.Join(repo, filepath.FromSlash(path)), content)
	}
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "--quiet", "-m", "library "+tag)
	gittest.Run(t, repo, "tag", tag)

	mux := http.NewServeMux()
	// Latest-release resolution for both identities: tests that name o/r
	// explicitly and code paths that resolve the official defaults.
	latest := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}
	mux.HandleFunc("/repos/o/r/releases/latest", latest)
	mux.HandleFunc("/repos/"+defaultTemplateOwner+"/"+defaultTemplateRepo+"/releases/latest", latest)
	lib := &testLibrary{repo: repo, server: httptest.NewServer(mux)}
	t.Cleanup(lib.server.Close)
	return lib
}

// addRelease commits additional files, tags a second release, and points the
// latest-release stub at it.
func (l *testLibrary) addRelease(t *testing.T, tag string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		gittest.WriteFile(t, filepath.Join(l.repo, filepath.FromSlash(path)), content)
	}
	gittest.Run(t, l.repo, "add", ".")
	gittest.Run(t, l.repo, "commit", "--quiet", "-m", "library "+tag)
	gittest.Run(t, l.repo, "tag", tag)

	mux := http.NewServeMux()
	latest := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}
	mux.HandleFunc("/repos/o/r/releases/latest", latest)
	mux.HandleFunc("/repos/"+defaultTemplateOwner+"/"+defaultTemplateRepo+"/releases/latest", latest)
	l.server.Config.Handler = mux
}

// failLatest makes latest-release resolution fail, as an unreachable GitHub
// would.
func (l *testLibrary) failLatest() {
	l.server.Config.Handler = http.NotFoundHandler()
}

// sourceOpts returns the seams a fetch call needs, over the given cache root
// and runner (nil for the real one). Owner and Repo stay zero: the fixture's
// identity is RepoURL, and callers under test resolve their own defaults.
func (l *testLibrary) sourceOpts(cacheRoot string, r command.Runner) SourceOptions {
	return SourceOptions{
		RepoURL:       "file://" + l.repo,
		CacheRoot:     cacheRoot,
		GitHubBaseURL: l.server.URL,
		Runner:        r,
	}
}

// readCacheFile reads a file out of the cached checkout for assertions.
func readCacheFile(t *testing.T, src *Source, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(src.Root(), filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
