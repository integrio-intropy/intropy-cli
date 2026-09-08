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

	// Owner and Repo select the template library; zero values target the
	// official library. Source carries the fetch seams (GitHubBaseURL
	// redirects the latest-release API call in tests).
	Owner  string
	Repo   string
	Source SourceOptions
}

// Library is a template library checked out on disk at one resolved version.
//
// It exists so a caller rendering several templates pays for one fetch and
// gets one version across all of them — a component's manifests and its system's
// shared manifests drifting apart by a release would be a subtle, ugly bug. It
// also keeps the on-disk layout of a template package private to this package.
type Library struct {
	Owner   string
	Repo    string
	Version string

	root string
}

// FetchLibrary resolves the release tag and ensures the cached checkout of
// the library at that tag.
func FetchLibrary(ctx context.Context, opts LibraryOptions) (*Library, error) {
	s := opts.Source
	s.Version, s.Stderr, s.UserAgent = opts.Version, opts.Stderr, opts.UserAgent
	if s.Owner == "" && s.Repo == "" {
		s.Owner, s.Repo = libraryIdentity(opts.Owner, opts.Repo)
	}
	src, err := FetchSource(ctx, s)
	if err != nil {
		return nil, err
	}
	owner, repo := s.Owner, s.Repo // applyDefaults ran inside FetchSource
	return &Library{Owner: owner, Repo: repo, Version: src.Version, root: src.Root()}, nil
}

// Close is retained for callers that hold a library across a session; the
// checkout lives in the shared cache, so it is a no-op.
func (l *Library) Close() {}

// Ref describes the fetched library, for logs and result records.
func (l *Library) Ref() string {
	return fmt.Sprintf("%s/%s@%s", l.Owner, l.Repo, l.Version)
}

// Open loads one template's manifest and returns it with its skeleton root.
func (l *Library) Open(name string) (*Template, string, error) {
	templateRoot, err := templateDir(&Source{Version: l.Version, root: l.root}, l.Owner, l.Repo, name)
	if err != nil {
		return nil, "", err
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

// List returns the sorted template directory names in the library — the same
// names Open, Describe and Create accept. It mirrors the standalone List for
// a caller that already fetched the release.
func (l *Library) List() ([]string, error) {
	return listTemplateDirs(l.root)
}

// Describe returns the machine-readable manifest of one template in the
// library — the same document the standalone Describe produces, without a
// second fetch. The library's resolved version is reported as the release.
func (l *Library) Describe(name string) (*DescribeResult, error) {
	tmpl, _, err := l.Open(name)
	if err != nil {
		return nil, err
	}
	return &DescribeResult{
		Template:     tmpl.Metadata.Name,
		Title:        tmpl.Metadata.Title,
		Description:  tmpl.Metadata.Description,
		Tags:         tmpl.Metadata.Tags,
		Labels:       tmpl.Metadata.Labels,
		Owner:        l.Owner,
		Repo:         l.Repo,
		Version:      l.Version,
		Parameters:   tmpl.Spec.Parameters,
		Dependencies: tmpl.Spec.Dependencies,
		Fields:       tmpl.Fields(),
	}, nil
}
