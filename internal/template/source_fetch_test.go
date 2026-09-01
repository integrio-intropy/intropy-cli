package template

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitclone"
)

var fetchTestFiles = map[string]string{
	"alpha/template.yaml":      testTemplateYAML,
	"alpha/skeleton/README.md": "alpha\n",
	"beta/template.yaml":       testTemplateYAML,
	"beta/skeleton/README.md":  "beta\n",
}

func TestFetchSourceClonesThenReusesCache(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()
	r := &recordingRunner{base: git.DefaultRunner()}
	var stderr bytes.Buffer

	opts := lib.sourceOpts(cache, r)
	opts.Version = "v1.0.0"
	opts.Stderr = &stderr
	src, err := FetchSource(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if src.Version != "v1.0.0" {
		t.Errorf("Version = %q", src.Version)
	}
	if got := readCacheFile(t, src, "alpha/skeleton/README.md"); got != "alpha\n" {
		t.Errorf("checkout content = %q", got)
	}
	if r.cloneCount() != 1 {
		t.Fatalf("clones = %d, want 1", r.cloneCount())
	}
	if !strings.Contains(stderr.String(), "fetching ") || !strings.Contains(stderr.String(), "@v1.0.0") {
		t.Errorf("stderr should announce the fetch: %q", stderr.String())
	}

	// Second fetch of the same tag: no clone, and the user is told the cache
	// answered.
	stderr.Reset()
	src, err = FetchSource(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.cloneCount() != 1 {
		t.Errorf("cache hit cloned again (clones = %d)", r.cloneCount())
	}
	if !strings.Contains(stderr.String(), "using cached ") || !strings.Contains(stderr.String(), "@v1.0.0") {
		t.Errorf("stderr should announce the cache hit: %q", stderr.String())
	}
}

// A pinned, cached version never touches the network: it is the
// fully-offline story.
func TestFetchSourcePinnedCachedVersionNeedsNoNetwork(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()

	opts := lib.sourceOpts(cache, nil)
	opts.Version = "v1.0.0"
	if _, err := FetchSource(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	// Fail both network paths: the API stub and the remote itself. The cache
	// key is the remote URL, so the URL stays put — the repository behind it
	// is what goes away.
	lib.failLatest()
	if err := os.Rename(lib.repo, lib.repo+"-moved"); err != nil {
		t.Fatal(err)
	}

	src, err := FetchSource(context.Background(), opts)
	if err != nil {
		t.Fatalf("pinned cached version should need no network: %v", err)
	}
	if src.Version != "v1.0.0" {
		t.Errorf("Version = %q", src.Version)
	}
}

func TestFetchSourceLatestResolutionFailureFallsBackToCache(t *testing.T) {
	lib := newTestLibrary(t, "v0.4.6", fetchTestFiles)
	lib.addRelease(t, "v0.5.0-rc.1", map[string]string{"rc.txt": "prerelease\n"})
	cache := t.TempDir()

	// Prime the cache with both tags via pinned fetches.
	for _, tag := range []string{"v0.4.6", "v0.5.0-rc.1"} {
		opts := lib.sourceOpts(cache, nil)
		opts.Version = tag
		if _, err := FetchSource(context.Background(), opts); err != nil {
			t.Fatalf("prime %s: %v", tag, err)
		}
	}

	lib.failLatest()
	var stderr bytes.Buffer
	opts := lib.sourceOpts(cache, nil)
	opts.Stderr = &stderr
	src, err := FetchSource(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Stable wins over the newer prerelease: the fallback exists to keep
	// scaffolding working, not to surprise.
	if src.Version != "v0.4.6" {
		t.Errorf("Version = %q, want the newest stable v0.4.6", src.Version)
	}
	if !strings.Contains(stderr.String(), "using cached templates v0.4.6") {
		t.Errorf("stderr should name the fallback: %q", stderr.String())
	}
}

// Clone debris and lock files are not release candidates: a failed clone's
// leftover temp dir must never be elected the newest cached tag.
func TestNewestCachedTagIgnoresDebris(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()

	opts := lib.sourceOpts(cache, nil)
	opts.Version = "v1.0.0"
	if _, err := FetchSource(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	url := "file://" + lib.repo
	repoDir := gitclone.CheckoutDir(cache, url)
	for _, debris := range []string{".tmp-v9.9.9-12345", "v9.9.9.lock"} {
		if err := os.MkdirAll(filepath.Join(repoDir, debris), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := newestCachedTag(cache, url); got != "v1.0.0" {
		t.Errorf("newestCachedTag = %q, want v1.0.0", got)
	}
}

func TestFetchSourceLatestFailureWithEmptyCache(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	lib.failLatest()

	_, err := FetchSource(context.Background(), lib.sourceOpts(t.TempDir(), nil))
	if err == nil {
		t.Fatal("expected an error when the cache cannot answer")
	}
	if !strings.Contains(err.Error(), "--template-version") {
		t.Errorf("error should name the remediation: %v", err)
	}
}

// The API answered but the clone did not — a partial outage. The newest
// cached release keeps scaffolding working, with both tags named.
func TestFetchSourceCloneFailureFallsBackToCache(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)

	// Clone v1.0.0 into the URL's cache entry, then make the remote
	// unreachable. The failing run resolves the stub's latest (v1.2.3), which
	// the cache does not hold, so the fallback answers with v1.0.0.
	url := "file://" + lib.repo
	cacheRoot := t.TempDir()
	dir := checkoutDir(cacheRoot, url, "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := git.CloneTagManaged(context.Background(), git.DefaultRunner(), url, dir, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lib.repo, lib.repo+"-moved"); err != nil {
		t.Fatal(err)
	}

	lib.failLatest()
	var stderr bytes.Buffer
	broken := lib.sourceOpts(cacheRoot, nil)
	broken.Stderr = &stderr
	src, err := FetchSource(context.Background(), broken)
	if err != nil {
		t.Fatal(err)
	}
	if src.Version != "v1.0.0" {
		t.Errorf("Version = %q, want the cached v1.0.0", src.Version)
	}
	if !strings.Contains(stderr.String(), "using cached templates v1.0.0") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// The requested tag is not cached and cannot be cloned: the fallback is a
// different release, which the stderr note must say plainly.
func TestFetchSourceCloneFailureFallsBackToADifferentTag(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)

	// The remote is reachable at prime time and gone at fetch time — the
	// partial-outage case. The URL is the cache key, so the dead remote keeps
	// the healthy run's URL: point the same URL at a path that never held a
	// repository by priming through a symlink that is later retargeted.
	// (Renaming the repository away does not work on APFS — the clone keeps
	// the directory valid through the rename.)
	broken := lib.sourceOpts(t.TempDir(), nil)
	cacheRoot := broken.CacheRoot
	dir := checkoutDir(cacheRoot, broken.RepoURL, "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := git.CloneTagManaged(context.Background(), git.DefaultRunner(), broken.RepoURL, dir, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	lib.addRelease(t, "v1.2.3", map[string]string{"newer.txt": "newer\n"})
	if err := os.RemoveAll(lib.repo); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	broken.Stderr = &stderr
	src, err := FetchSource(context.Background(), broken)
	if err != nil {
		t.Fatal(err)
	}
	if src.Version != "v1.0.0" {
		t.Errorf("Version = %q, want the cached v1.0.0", src.Version)
	}
	if !strings.Contains(stderr.String(), "cannot fetch") ||
		!strings.Contains(stderr.String(), "@v1.2.3") ||
		!strings.Contains(stderr.String(), "using cached templates v1.0.0") {
		t.Errorf("stderr should name both tags: %q", stderr.String())
	}
}

func TestFetchSourceCloneFailureWithEmptyCache(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	opts := lib.sourceOpts(t.TempDir(), nil)
	opts.Version = "v9.9.9" // pinned, so resolution is not the failure

	_, err := FetchSource(context.Background(), opts)
	if err == nil {
		t.Fatal("expected an error for an unclonable tag with an empty cache")
	}
	for _, want := range []string{"v9.9.9", "--template-version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// A held lock means another run is mid-clone: wait rather than queue.
func TestFetchSourceLockHeld(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()
	url := "file://" + lib.repo
	dir := filepath.Join(gitclone.CheckoutDir(cache, url), "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := gitclone.Lock(dir+".lock", "scaffold", "template cache")
	if err != nil {
		t.Fatal(err)
	}
	defer gitclone.Unlock(lock)

	opts := lib.sourceOpts(cache, nil)
	opts.Version = "v1.0.0"
	_, err = FetchSource(context.Background(), opts)
	if err == nil {
		t.Fatal("expected a wait error while another run holds the lock")
	}
	if !strings.Contains(err.Error(), "already using") {
		t.Errorf("error should explain another run holds the cache: %v", err)
	}
}

// A clone killed before the rename leaves only a temp sibling; the next run
// must not treat it as an entry.
func TestFetchSourceIgnoresInterruptedCloneDebris(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()
	url := "file://" + lib.repo
	repoDir := gitclone.CheckoutDir(cache, url)
	debris := filepath.Join(repoDir, ".tmp-v1.0.0-999")
	if err := os.MkdirAll(debris, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(debris, "partial.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := lib.sourceOpts(cache, nil)
	opts.Version = "v1.0.0"
	src, err := FetchSource(context.Background(), opts)
	if err != nil {
		t.Fatalf("should have cloned past the debris: %v", err)
	}
	if got := readCacheFile(t, src, "alpha/skeleton/README.md"); got != "alpha\n" {
		t.Errorf("checkout content = %q", got)
	}
}

// failingRunner makes every clone fail, to prove the fallback does not depend
// on git's error text.
type failingRunner struct{ err error }

func (r failingRunner) Run(context.Context, string, string, ...string) ([]byte, []byte, error) {
	return nil, nil, r.err
}

func TestFetchSourceCloneFailureIsRunnerAgnostic(t *testing.T) {
	lib := newTestLibrary(t, "v1.0.0", fetchTestFiles)
	cache := t.TempDir()

	opts := lib.sourceOpts(cache, nil)
	opts.Version = "v1.0.0"
	if _, err := FetchSource(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	broken := lib.sourceOpts(cache, failingRunner{err: errors.New("network unreachable")})
	broken.Stderr = &stderr
	src, err := FetchSource(context.Background(), broken)
	if err != nil {
		t.Fatal(err)
	}
	if src.Version != "v1.0.0" {
		t.Errorf("Version = %q, want the cached v1.0.0", src.Version)
	}
}

var _ command.Runner = failingRunner{}
