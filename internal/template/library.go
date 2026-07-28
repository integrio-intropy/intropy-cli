package template

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DefaultLibrary reports the official template library the CLI targets.
//
// Callers outside this package need it to describe what they fetched; the
// constants themselves stay unexported so defaults_test.go remains the one place
// a change to the library source has to be acknowledged.
func DefaultLibrary() (owner, repo string) {
	return defaultTemplateOwner, defaultTemplateRepo
}

// LibraryOptions selects which template library to fetch.
type LibraryOptions struct {
	// Version pins a release tag. Empty resolves the latest release.
	Version string

	HTTP      *http.Client
	UserAgent string
	Stderr    io.Writer

	// Test overrides. Production callers leave these zero-valued.
	Owner         string
	Repo          string
	GitHubBaseURL string
}

// Library is a template library extracted on disk at one resolved version.
//
// It exists so a caller rendering several templates pays for one download and
// gets one version across all of them — a component's manifests and its system's
// shared manifests drifting apart by a release would be a subtle, ugly bug. It
// also keeps the on-disk layout of a template package private to this package.
//
// The caller owns the returned Library and must Close it.
type Library struct {
	Owner   string
	Repo    string
	Version string

	root    string
	cleanup func()
}

// FetchLibrary resolves the release tag, downloads the library tarball and
// extracts it.
func FetchLibrary(ctx context.Context, opts LibraryOptions) (*Library, error) {
	owner, repo := DefaultLibrary()
	if opts.Owner != "" {
		owner = opts.Owner
	}
	if opts.Repo != "" {
		repo = opts.Repo
	}
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "intropy-cli"
	}

	gh := newConfiguredGitHub(opts.HTTP, userAgent, opts.GitHubBaseURL)
	tag, err := gh.ResolveTag(ctx, owner, repo, opts.Version)
	if err != nil {
		return nil, err
	}
	if opts.Stderr != nil {
		fmt.Fprintf(opts.Stderr, "fetching %s/%s@%s\n", owner, repo, tag)
	}

	rc, err := gh.Tarball(ctx, owner, repo, tag)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	tmpDir, err := os.MkdirTemp("", "intropy-library-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	if err := ExtractTarGz(rc, tmpDir); err != nil {
		cleanup()
		return nil, err
	}
	return &Library{Owner: owner, Repo: repo, Version: tag, root: tmpDir, cleanup: cleanup}, nil
}

// Close removes the extracted copy.
func (l *Library) Close() {
	if l != nil && l.cleanup != nil {
		l.cleanup()
	}
}

// Ref describes the fetched library, for logs and result records.
func (l *Library) Ref() string {
	return fmt.Sprintf("%s/%s@%s", l.Owner, l.Repo, l.Version)
}

// Open loads one template's manifest and returns it with its skeleton root.
//
// The name is validated as a single path segment before it is joined, because it
// is user input turned into a path inside the extracted tarball.
func (l *Library) Open(name string) (*Template, string, error) {
	if err := validateTemplateName(name); err != nil {
		return nil, "", err
	}
	templateRoot := filepath.Join(l.root, name)
	if info, err := os.Stat(templateRoot); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("template %q not found in %s", name, l.Ref())
	}
	tmpl, err := LoadTemplate(filepath.Join(templateRoot, templateManifestName))
	if err != nil {
		return nil, "", fmt.Errorf("%s in %s: %w", name, l.Ref(), err)
	}
	skeleton := filepath.Join(templateRoot, templateSkeletonDir)
	if info, err := os.Stat(skeleton); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("template %q is missing %s/ directory", name, templateSkeletonDir)
	}
	return tmpl, skeleton, nil
}
