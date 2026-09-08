// Package templatetest builds real git-backed template libraries for tests of
// packages that consume internal/template. It mirrors the fixture the template
// package's own tests use: a git repository tagged with a release, an API stub
// for latest-release resolution, and SourceOptions seams pointed at both.
//
// Real git rather than a tarball server: clone is exactly the operation where
// a mock encodes our assumptions instead of git's behaviour, and it is what
// production runs.
package templatetest

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// Library is a git-backed template library fixture.
type Library struct {
	repo   string
	Server *httptest.Server

	// LatestRequests counts latest-release API calls, so a test can prove a
	// pinned version skips resolution entirely.
	LatestRequests *atomic.Int32
}

// NewLibrary commits files (repo-relative paths like
// "deploy-host/template.yaml") on main, tags the commit with tag, and starts
// the latest-release API stub. The stub answers for both the conventional
// test identity o/r and the official library defaults, since consumer code
// under test resolves either.
func NewLibrary(t *testing.T, tag string, files map[string]string) *Library {
	t.Helper()
	repo := gittest.NewRepo(t, "main")
	for path, content := range files {
		gittest.WriteFile(t, filepath.Join(repo, filepath.FromSlash(path)), content)
	}
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "--quiet", "-m", "library "+tag)
	gittest.Run(t, repo, "tag", tag)

	owner, name := template.DefaultLibrary()
	mux := http.NewServeMux()
	var hits atomic.Int32
	latest := func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}
	mux.HandleFunc("/repos/o/r/releases/latest", latest)
	mux.HandleFunc("/repos/"+owner+"/"+name+"/releases/latest", latest)
	lib := &Library{repo: repo, Server: httptest.NewServer(mux), LatestRequests: &hits}
	t.Cleanup(lib.Server.Close)
	return lib
}

// Source returns the fetch seams pointed at the fixture, with the cache
// rooted in a fresh temporary directory. Owner and Repo stay zero: the
// fixture's identity is its URL, and the code under test resolves its own
// defaults — exactly what production does.
func (l *Library) Source(t *testing.T) template.SourceOptions {
	t.Helper()
	return template.SourceOptions{
		RepoURL:       "file://" + l.repo,
		CacheRoot:     t.TempDir(),
		GitHubBaseURL: l.Server.URL,
	}
}

// SourceWithTag returns the seams for one additional tag of the same library,
// for tests that pin a non-latest release. The content is identical; the tag
// exists only to be asked for explicitly.
func (l *Library) SourceWithTag(t *testing.T, tag string) template.SourceOptions {
	t.Helper()
	gittest.Run(t, l.repo, "tag", "-f", tag)
	return l.Source(t)
}
