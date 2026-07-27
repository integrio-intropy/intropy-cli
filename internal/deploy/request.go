package deploy

import (
	"io"
	"os"

	"github.com/integrio-intropy/intropy-cli/internal/command"
)

const (
	OutputPlain = "plain"
	OutputJSON  = "json"
)

// Options configures Run.
type Options struct {
	// Component is the component name; Domain and System disambiguate it when
	// the name occurs more than once.
	Component string
	Domain    string
	System    string

	// Environment is the target environment. Required.
	Environment string

	// GitopsRepo overrides the configured GitOps repository URL.
	GitopsRepo string

	// SourceDir is the source repository to read HEAD from; defaults to the
	// working directory.
	SourceDir string

	// PlanOnly stops after the diff, writing nothing to git.
	PlanOnly bool

	// AllowDirty permits uncommitted changes under the component's sourcePaths.
	AllowDirty bool

	// OutputFormat is "plain" or "json".
	OutputFormat string

	// Color enables ANSI colour in the diff.
	Color bool

	// CacheRoot overrides where GitOps checkouts are cached.
	CacheRoot string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o *Options) applyDefaults() {
	if o.Runner == nil {
		o.Runner = command.ExecRunner{}
	}
	if o.SourceDir == "" {
		o.SourceDir = "."
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
