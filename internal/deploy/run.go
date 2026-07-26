package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/integrio-intropy/intropy-cli/internal/config"
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

	Runner    Runner
	UserAgent string
	Stdout    io.Writer
	Stderr    io.Writer
}

// Result is the machine-readable outcome. Field names are stable and
// additive-only.
type Result struct {
	Component    string      `json:"component"`
	Domain       string      `json:"domain"`
	System       string      `json:"system"`
	Environment  string      `json:"environment"`
	SourceCommit string      `json:"sourceCommit"`
	AppName      string      `json:"appName"`
	OverlayPath  string      `json:"overlayPath"`
	Pins         []ResultPin `json:"pins"`
	Changed      bool        `json:"changed"`
	Applied      bool        `json:"applied"`
	SyncPolicy   string      `json:"syncPolicy"`
}

// ResultPin is one image's before and after state.
type ResultPin struct {
	Image    string `json:"image"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`
	Tag      string `json:"tag"`
}

const (
	OutputPlain = "plain"
	OutputJSON  = "json"
)

func (o *Options) applyDefaults() {
	if o.Runner == nil {
		o.Runner = ExecRunner{}
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

// Run resolves the component's digest for the current commit and pins it into
// one environment's overlay.
//
// With PlanOnly set it stops after the diff, having written nothing. Otherwise
// this branch stops there too — committing and pushing arrives with the next
// step — so the overlay edits are always reverted before returning.
func Run(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	// Fail before touching the network or the cache if the tools are absent:
	// discovering a missing kustomize after cloning wastes the user's time.
	if err := RequireBinaries("git", "kustomize"); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resolved := cfg.Resolve(config.Flags{GitopsRepo: opts.GitopsRepo})
	repoURL, err := resolved.RequireGitopsRepo()
	if err != nil {
		return err
	}

	fmt.Fprintf(opts.Stderr, "refreshing %s\n", repoURL)
	wt, err := OpenWorktree(ctx, WorktreeOptions{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return err
	}
	defer wt.Close()

	deployCfg, err := LoadDeployConfig(wt.Root)
	if err != nil {
		return err
	}
	env, err := deployCfg.Environment(opts.Environment)
	if err != nil {
		return err
	}

	coord, err := FindComponent(wt.Root, opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	compDir := componentDir(wt.Root, coord)
	comp, err := LoadComponentConfig(compDir)
	if err != nil {
		return err
	}
	overlayDir, err := ResolveOverlay(wt.Root, coord, comp, opts.Environment)
	if err != nil {
		return err
	}

	source, err := InspectSource(ctx, Git{Runner: opts.Runner, Dir: opts.SourceDir}, comp.SourcePaths, opts.AllowDirty)
	if err != nil {
		return err
	}
	if len(source.Dirty) > 0 {
		fmt.Fprintf(opts.Stderr, "warning: deploying with %d uncommitted change(s) under the component's source paths\n", len(source.Dirty))
	}

	resolver, err := NewResolver(opts.UserAgent)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "resolving %s\n", CommitTag(source.ShortCommit()))
	pins, err := ResolveDigests(ctx, resolver, comp, source.Commit)
	if err != nil {
		return err
	}

	palette := PlainPalette
	if opts.Color {
		palette = ColorPalette
	}
	plan, err := BuildPlan(ctx, PlanOptions{
		Worktree:    wt,
		Kustomize:   Kustomize{Runner: opts.Runner},
		Coordinate:  coord,
		Environment: opts.Environment,
		Source:      source,
		Pins:        pins,
		OverlayDir:  overlayDir,
		Palette:     palette,
	})
	if err != nil {
		return err
	}

	// Nothing here commits yet, so never leave the shared checkout dirty.
	if !plan.Empty() {
		defer func() {
			if rerr := plan.Revert(ctx, wt); rerr != nil {
				fmt.Fprintf(opts.Stderr, "warning: could not revert the overlay edit: %v\n", rerr)
			}
		}()
	}

	return report(opts, plan, coord, env)
}

func componentDir(root string, c Coordinate) string {
	return joinRel(root, c.RelPath())
}

func report(opts Options, plan *Plan, coord Coordinate, env EnvironmentConfig) error {
	if opts.OutputFormat == OutputJSON {
		return writeJSON(opts, plan, coord, env)
	}

	if plan.Empty() {
		fmt.Fprintf(opts.Stdout, "%s is already at %s in %s; nothing to do\n",
			coord, plan.Pins[0].Digest, plan.Environment)
		return nil
	}

	fmt.Fprint(opts.Stdout, plan.Summary())
	if plan.ProvenanceOnly() {
		// kustomize propagates commonAnnotations into pod templates, so this
		// still restarts the pods. Say so rather than let it surprise anyone.
		fmt.Fprintf(opts.Stdout, "\nno image digest changed; only the source-commit annotation moves.\nthis still restarts the pods, because kustomize applies commonAnnotations to pod templates.\n")
	}
	fmt.Fprintf(opts.Stdout, "\n%s", plan.Diff)

	if opts.PlanOnly {
		fmt.Fprintf(opts.Stderr, "\nplan only: nothing was committed\n")
	} else {
		// Committing and pushing is not wired up in this step.
		fmt.Fprintf(opts.Stderr, "\nthe overlay edit was reverted: committing and pushing is not implemented yet — use --plan to silence this\n")
	}
	return nil
}

func writeJSON(opts Options, plan *Plan, coord Coordinate, env EnvironmentConfig) error {
	res := Result{
		Component:    coord.Component,
		Domain:       coord.Domain,
		System:       coord.System,
		Environment:  plan.Environment,
		SourceCommit: plan.Source.Commit,
		AppName:      coord.AppName(plan.Environment),
		OverlayPath:  coord.OverlayRelPath(plan.Environment),
		Changed:      !plan.Empty(),
		Applied:      false,
		SyncPolicy:   env.Sync,
	}
	for _, pin := range plan.Pins {
		res.Pins = append(res.Pins, ResultPin{
			Image:    pin.Image,
			Previous: plan.Previous[pin.Image],
			Digest:   pin.Digest,
			Tag:      pin.Tag,
		})
	}

	enc := json.NewEncoder(opts.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("write JSON result: %w", err)
	}
	return nil
}
