package release

import (
	"io"
	"os"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
)

const (
	OutputPlain = "plain"
	OutputJSON  = "json"
)

// Options configures Create and View.
type Options struct {
	// Component is the component name; Domain and System disambiguate it when
	// the name occurs more than once.
	Component string
	Domain    string
	System    string

	// Version is the release version. Required by Create; View takes it as a
	// positional argument instead.
	Version string

	// Ref is the source revision to release. Defaults to HEAD.
	Ref string

	// Since names the starting point for the changelog when there is no
	// previous release to measure from — the adoption case, where a component
	// has been deployed by hand for years before its first managed release.
	Since string

	// GitopsRepo overrides the configured GitOps repository URL.
	GitopsRepo string

	// SourceDir is the source repository to read from; defaults to the working
	// directory.
	SourceDir string

	// AllowDirty permits uncommitted changes under the component's sourcePaths.
	AllowDirty bool

	// OutputFormat is "plain" or "json".
	OutputFormat string

	// CacheRoot overrides where GitOps checkouts are cached.
	CacheRoot string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o *Options) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
	}
	if o.SourceDir == "" {
		o.SourceDir = "."
	}
	if o.Ref == "" {
		o.Ref = "HEAD"
	}
	if o.OutputFormat == "" {
		o.OutputFormat = OutputPlain
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
}

// Result is the machine-readable outcome of Create.
//
// Field names are stable and additive-only.
type Result struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	Ref       string `json:"ref"`
	Digest    string `json:"digest"`

	// Created is false when the release already existed and this run
	// recognised its own earlier work rather than publishing again.
	Created bool `json:"created"`

	// Tag is the annotated git tag, and Tagged reports whether it reached the
	// remote. A release is valid without its tag; the tag is for people
	// reading git log.
	Tag    string `json:"tag"`
	Tagged bool   `json:"tagged"`

	Manifest *Manifest `json:"manifest"`
}
