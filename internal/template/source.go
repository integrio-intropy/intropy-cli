package template

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitclone"
)

// SourceOptions configures how the template library is fetched. It carries the
// fields every fetch path shares so Create, Describe, List, FetchLibrary and
// PrepareCreate resolve and cache the library identically.
type SourceOptions struct {
	// Version pins a release tag. Empty resolves the latest release.
	Version string

	// Owner and Repo select the template library; zero values target the
	// official library.
	Owner string
	Repo  string

	Stderr    io.Writer
	UserAgent string

	// The remaining fields are test-only seams. GitHubBaseURL redirects the
	// latest-release API call; RepoURL overrides the git URL the library is
	// cloned from (a file:// fixture); CacheRoot and Runner redirect the
	// checkout cache.
	GitHubBaseURL string
	RepoURL       string
	CacheRoot     string
	Runner        command.Runner
}

func (o *SourceOptions) applyDefaults() {
	if o.Owner == "" {
		o.Owner = defaultTemplateOwner
	}
	if o.Repo == "" {
		o.Repo = defaultTemplateRepo
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// Source is the template library as a local checkout: it knows which library
// and release it holds and where that checkout's root is.
type Source struct {
	// Owner and Repo are the resolved library identity — defaults applied —
	// for scaffold records, results and messages.
	Owner string
	Repo  string

	// Version is the release tag the checkout holds — the resolved tag, which
	// is not necessarily the one first asked for when the fallback used the
	// cache.
	Version string

	root string
}

// Root returns the root of the library checkout. Template subdirectories live
// directly beneath it.
func (s *Source) Root() string { return s.root }

// FetchSource resolves the release tag and ensures a local checkout of the
// library at that tag, cloning shallowly on first use and reusing the cache
// after. When the network is unavailable it falls back to the newest cached
// release, and says so on stderr.
func FetchSource(ctx context.Context, opts SourceOptions) (*Source, error) {
	opts.applyDefaults()
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	cacheRoot, err := templateCacheRoot(opts.CacheRoot)
	if err != nil {
		return nil, err
	}
	url := templateRepoURL(opts)

	gh := newConfiguredGitHub(nil, opts.UserAgent, opts.GitHubBaseURL)
	tag, err := gh.ResolveTag(ctx, opts.Owner, opts.Repo, opts.Version)
	if err != nil {
		// A pinned version is a promise to build exactly that; silently
		// building a different cached tag would break it. Latest-release
		// resolution makes no such promise, so the cache may answer.
		if opts.Version != "" {
			return nil, err
		}
		return cachedFallback(stderr, cacheRoot, url, opts.Owner, opts.Repo, "resolve the latest release", err)
	}

	root, hit, err := ensureCheckout(ctx, opts, cacheRoot, url, tag)
	if err != nil {
		fallback := newestCachedTag(cacheRoot, url)
		if fallback == "" {
			return nil, fmt.Errorf("%w\npin a cached release with --template-version (cache: %s)", err, gitclone.CheckoutDir(cacheRoot, url))
		}
		// A cache hit on the tag that just failed to clone is no fallback at
		// all — it is the answer to this exact request, and the ordinary hit
		// note keeps that straight.
		if fallback == tag {
			fmt.Fprintf(stderr, "using cached %s/%s@%s\n", opts.Owner, opts.Repo, tag)
		} else {
			fmt.Fprintf(stderr, "cannot fetch %s/%s@%s; using cached templates %s\n", opts.Owner, opts.Repo, tag, fallback)
		}
		return &Source{Owner: opts.Owner, Repo: opts.Repo, Version: fallback, root: checkoutDir(cacheRoot, url, fallback)}, nil
	}
	if hit {
		fmt.Fprintf(stderr, "using cached %s/%s@%s\n", opts.Owner, opts.Repo, tag)
	} else {
		fmt.Fprintf(stderr, "fetching %s/%s@%s\n", opts.Owner, opts.Repo, tag)
	}
	return &Source{Owner: opts.Owner, Repo: opts.Repo, Version: tag, root: root}, nil
}

// libraryIdentity resolves the caller's owner/repo against the defaults.
// Every public entry point runs it, so a test fixture that leaves Owner/Repo
// zero — its identity is RepoURL — still lands on the official library in
// records and messages, exactly as production does.
func libraryIdentity(owner, repo string) (string, string) {
	if owner == "" {
		owner = defaultTemplateOwner
	}
	if repo == "" {
		repo = defaultTemplateRepo
	}
	return owner, repo
}

// templateRepoURL names the git remote the library is cloned from. The URL is
// https rather than the API's tarball endpoint: the clone goes through the
// user's git credentials and never through codeload, whose anonymous rate
// limits a token cannot reach across the redirect.
func templateRepoURL(opts SourceOptions) string {
	if opts.RepoURL != "" {
		return opts.RepoURL
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", opts.Owner, opts.Repo)
}

func templateCacheRoot(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return gitclone.CacheRoot("templates")
}

// checkoutDir is the cache entry for one immutable tag. Entries are
// write-once: a tag's tree never changes, so existence is validity and no
// refresh or origin verification ever runs on a hit.
func checkoutDir(cacheRoot, url, tag string) string {
	return filepath.Join(gitclone.CheckoutDir(cacheRoot, url), tag)
}

// ensureCheckout returns the cache entry for tag, cloning shallowly on a miss.
// The clone lands in a dot-prefixed temp sibling and is renamed into place, so
// an interrupted clone can never be observed as a complete entry — and never
// elected by newestCachedTag, which skips dot-prefixed names.
func ensureCheckout(ctx context.Context, opts SourceOptions, cacheRoot, url, tag string) (root string, cached bool, err error) {
	dir := checkoutDir(cacheRoot, url, tag)
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return dir, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", false, fmt.Errorf("create cache directory: %w", err)
	}

	lock, err := gitclone.Lock(dir+".lock", "scaffold", "template cache")
	if err != nil {
		return "", false, err
	}
	defer gitclone.Unlock(lock)

	// Re-check under the lock: the run that held it may have done the clone.
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return dir, true, nil
	}

	r := opts.Runner
	if r == nil {
		r = git.DefaultRunner()
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".tmp-"+tag+"-*")
	if err != nil {
		return "", false, fmt.Errorf("create cache directory: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := git.CloneTagManaged(ctx, r, url, tmp, tag); err != nil {
		return "", false, fmt.Errorf("clone %s/%s@%s: %w", opts.Owner, opts.Repo, tag, err)
	}
	// A failed rename after a successful clone is retried as a cache miss next
	// run — the entry is still write-once either way.
	if err := os.Rename(tmp, dir); err != nil {
		return "", false, fmt.Errorf("install cache entry %s: %w", dir, err)
	}
	return dir, false, nil
}

// newestCachedTag returns the newest stable release tag held in the cache for
// url, or "" when the cache is empty. Stable wins over prerelease: the
// fallback exists to keep scaffolding working, and a prerelease is a
// surprising thing to be handed silently. Entries that are not release-shaped
// directories — lock files, clone debris — are not candidates.
func newestCachedTag(cacheRoot, url string) string {
	ents, err := os.ReadDir(gitclone.CheckoutDir(cacheRoot, url))
	if err != nil {
		return ""
	}
	var stable, pre []*semver.Version
	for _, ent := range ents {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		v, err := semver.NewVersion(ent.Name())
		if err != nil {
			continue
		}
		if v.Prerelease() == "" {
			stable = append(stable, v)
		} else {
			pre = append(pre, v)
		}
	}
	candidates := stable
	if len(candidates) == 0 {
		candidates = pre
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Sort(semver.Collection(candidates))
	return candidates[len(candidates)-1].Original()
}

// templateDir locates one template within a source checkout, validating the
// name before it becomes a path — it is user input.
func templateDir(src *Source, owner, repo, name string) (string, error) {
	if err := validateTemplateName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(src.root, name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("template %q not found in %s/%s@%s", name, owner, repo, src.Version)
	}
	return dir, nil
}

// cachedFallback answers a failed latest-release resolution from the cache.
// The failure is only reported when the cache cannot answer at all — with the
// remediation on the second line, per house style.
func cachedFallback(stderr io.Writer, cacheRoot, url, owner, repo, doing string, cause error) (*Source, error) {
	cached := newestCachedTag(cacheRoot, url)
	if cached == "" {
		return nil, fmt.Errorf("%w\npin a cached release with --template-version (cache: %s)", cause, gitclone.CheckoutDir(cacheRoot, url))
	}
	fmt.Fprintf(stderr, "cannot %s; using cached templates %s\n", doing, cached)
	return &Source{Owner: owner, Repo: repo, Version: cached, root: checkoutDir(cacheRoot, url, cached)}, nil
}
