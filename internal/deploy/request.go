package deploy

import (
	"io"
	"os"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
)

const (
	OutputPlain = "plain"
	OutputJSON  = "json"
)

// output is where a command writes, and in what form. Every command in this
// package reports the same way, so the reporting takes this rather than one of
// the three Options types.
type output struct {
	Format string
	Color  bool
	Stdout io.Writer
	Stderr io.Writer
}

// palette picks the diff colouring.
func (o output) palette() kustomize.Palette {
	if o.Color {
		return kustomize.ColorPalette
	}
	return kustomize.PlainPalette
}

// Options configures Run.
type Options struct {
	// Component is the component name; Domain and System disambiguate it when
	// the name occurs more than once.
	Component string
	Domain    string
	System    string

	// Environment is the target environment. Required.
	Environment string

	// Version selects a published release to deploy. Empty means the current
	// commit in SourceDir.
	//
	// With a version the release manifest is authoritative: it already records
	// the digests, so no source repository is read at all and the command works
	// from any directory. AllowDirty is meaningless in that mode, and the
	// command rejects the combination rather than ignoring it.
	Version string

	// GitopsRepo overrides the configured GitOps repository URL.
	GitopsRepo string

	// SourceDir is the source repository to read HEAD from; defaults to the
	// working directory.
	SourceDir string

	// PlanOnly stops after the diff, writing nothing to git.
	PlanOnly bool

	// AllowDirty permits uncommitted changes under the component's sourcePaths.
	AllowDirty bool

	// NoWait skips the ArgoCD wait after pushing.
	NoWait bool

	// Timeout bounds the ArgoCD wait. Zero means the package default.
	Timeout time.Duration

	// ArgocdServer overrides deploy.yaml's argocd.server.
	ArgocdServer string

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

func (o Options) output() output {
	return output{Format: o.OutputFormat, Color: o.Color, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o Options) session() sessionOptions {
	return sessionOptions{
		GitopsRepo:   o.GitopsRepo,
		ArgocdServer: o.ArgocdServer,
		CacheRoot:    o.CacheRoot,
		Runner:       o.Runner,
		Stderr:       o.Stderr,
	}
}

func (o *Options) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
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

// PromoteOptions configures Promote.
//
// Deliberately narrower than Options: there is no Version, no SourceDir and no
// AllowDirty, because a promotion reads neither a source repository nor a
// registry. That absence is the feature — it is what makes it impossible for a
// promotion to pin bits the source environment never ran.
type PromoteOptions struct {
	Component string
	Domain    string
	System    string

	// From is the environment to copy the pinned digests out of, and To the one
	// to write them into. Both required.
	From string
	To   string

	GitopsRepo string

	// PlanOnly stops after the diff, writing nothing to git.
	PlanOnly bool

	// NoWait skips the ArgoCD wait after pushing. A manual-sync target never
	// waits regardless.
	NoWait bool

	Timeout      time.Duration
	ArgocdServer string
	OutputFormat string
	Color        bool
	CacheRoot    string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o PromoteOptions) output() output {
	return output{Format: o.OutputFormat, Color: o.Color, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o PromoteOptions) session() sessionOptions {
	return sessionOptions{
		GitopsRepo:   o.GitopsRepo,
		ArgocdServer: o.ArgocdServer,
		CacheRoot:    o.CacheRoot,
		Runner:       o.Runner,
		Stderr:       o.Stderr,
	}
}

func (o *PromoteOptions) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
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

// DiffOptions configures Diff.
//
// There is no NoWait, Timeout, PlanOnly or Revision here: this command reads two
// renders and prints the difference. It waits for nothing, writes nothing, and
// has no revision of its own to reconcile — the revision it reports is the one
// the environment already has pending.
type DiffOptions struct {
	Component string
	Domain    string
	System    string

	// Environment is the environment to review. Required.
	Environment string

	GitopsRepo   string
	ArgocdServer string
	OutputFormat string

	// Color enables ANSI colour in the diff.
	Color bool

	CacheRoot string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o DiffOptions) output() output {
	return output{Format: o.OutputFormat, Color: o.Color, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o DiffOptions) session() sessionOptions {
	return sessionOptions{
		GitopsRepo:   o.GitopsRepo,
		ArgocdServer: o.ArgocdServer,
		CacheRoot:    o.CacheRoot,
		Runner:       o.Runner,
		Stderr:       o.Stderr,
	}
}

func (o *DiffOptions) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
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
	// Unlike every other command here, the diff text travels inside the JSON
	// rather than beside it, and ANSI escapes in a JSON string are not a diff
	// anyone can read.
	if o.OutputFormat == OutputJSON {
		o.Color = false
	}
}

// StatusOptions configures Status.
//
// Narrower than every other options type here: there is no Environment, because
// status is about all of them at once — the question it answers is whether the
// environments agree, which cannot be asked of one. There is no Color either,
// since it renders a table rather than a diff.
type StatusOptions struct {
	Component string
	Domain    string
	System    string

	GitopsRepo   string
	ArgocdServer string
	OutputFormat string
	CacheRoot    string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o StatusOptions) output() output {
	return output{Format: o.OutputFormat, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o StatusOptions) session() sessionOptions {
	return sessionOptions{
		GitopsRepo:   o.GitopsRepo,
		ArgocdServer: o.ArgocdServer,
		CacheRoot:    o.CacheRoot,
		Runner:       o.Runner,
		Stderr:       o.Stderr,
	}
}

func (o *StatusOptions) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
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

// SyncOptions configures Sync.
type SyncOptions struct {
	Component string
	Domain    string
	System    string

	// Environment is the environment to apply. Required.
	Environment string

	// Revision, when set, is the commit the caller reviewed. The sync is refused
	// if the environment's pending change is a different one, which is what
	// stops an approval being spent on a diff nobody looked at.
	Revision string

	GitopsRepo string
	NoWait     bool

	Timeout      time.Duration
	ArgocdServer string
	OutputFormat string
	CacheRoot    string

	Runner    command.Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

func (o SyncOptions) output() output {
	return output{Format: o.OutputFormat, Stdout: o.Stdout, Stderr: o.Stderr}
}

func (o SyncOptions) session() sessionOptions {
	return sessionOptions{
		GitopsRepo:   o.GitopsRepo,
		ArgocdServer: o.ArgocdServer,
		CacheRoot:    o.CacheRoot,
		Runner:       o.Runner,
		Stderr:       o.Stderr,
	}
}

func (o *SyncOptions) applyDefaults() {
	if o.Runner == nil {
		o.Runner = git.DefaultRunner()
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
