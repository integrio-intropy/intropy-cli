package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// now is the clock, replaced in tests so a manifest is reproducible.
var now = time.Now

// Create publishes an immutable release manifest and pushes an annotated git
// tag. It changes no environment: what was running before is still running,
// and because the version resolves the same commit it resolves the same
// digests.
//
// Re-running for a version that already exists is safe. The existing manifest
// is compared against what this run would publish; if they agree, the release
// is left alone and only a missing git tag is repaired. If they disagree the
// version is being reused for something else, and that is refused.
func Create(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	if opts.Version == "" {
		return errors.New("a version is required")
	}
	if err := command.RequireBinaries("git"); err != nil {
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
	repo, err := gitops.Open(ctx, gitops.Options{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return err
	}
	defer repo.Close()

	coord, err := gitops.FindComponent(repo.Root, opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	comp, err := gitops.LoadComponentConfig(gitops.JoinRel(repo.Root, coord.RelPath()))
	if err != nil {
		return err
	}
	if len(comp.Images) == 0 {
		return fmt.Errorf("%s declares no images, so there is nothing to release", coord)
	}

	g := git.Client{Runner: opts.Runner, Dir: opts.SourceDir}

	src, err := source.Inspect(ctx, g, comp.SourcePaths, opts.AllowDirty)
	if err != nil {
		return err
	}
	if len(src.Dirty) > 0 {
		fmt.Fprintf(opts.Stderr, "warning: releasing with %d uncommitted change(s) under the component's source paths\n", len(src.Dirty))
	}

	// Ref defaults to HEAD, which Inspect has already checked. A different ref
	// is resolved here and checked for reachability the same way.
	commit := src.Commit
	if opts.Ref != "HEAD" {
		commit, err = g.RevParse(ctx, opts.Ref)
		if err != nil {
			return fmt.Errorf("resolve --ref %s: %w", opts.Ref, err)
		}
		pushed, err := g.IsAncestor(ctx, commit, gitops.RemoteName+"/"+src.Branch)
		if err != nil {
			return err
		}
		if !pushed {
			return &source.UnpushedCommitError{Commit: commit, Branch: gitops.RemoteName + "/" + src.Branch}
		}
	}

	reg, err := NewRegistry(opts.UserAgent)
	if err != nil {
		return err
	}

	releasesRepo := ReleasesRepo(comp.Images[0].Name)
	ref := Ref(releasesRepo, opts.Version)

	// Look before publishing. A retry after a partial run has to recognise its
	// own earlier work, and that is only possible while the existing manifest
	// is still there to compare against.
	existing, err := lookup(ctx, reg, ref)
	if err != nil {
		return err
	}

	// Registry's Resolve matches source.Resolver, so no adapter is needed.
	var pins []source.Pin
	if opts.Watch {
		pins, err = source.WatchResolveDigests(ctx, reg, comp, commit, source.WatchOptions{Stderr: opts.Stderr})
	} else {
		pins, err = source.ResolveDigests(ctx, reg, comp, commit)
	}
	if err != nil {
		return err
	}

	basis, from, err := resolveBasis(ctx, reg, g, releasesRepo, commit, opts.Since, opts.Version)
	if err != nil {
		return err
	}

	var changes []Change
	if from != "" {
		if changes, err = Changelog(ctx, g, from, commit, comp.SourcePaths); err != nil {
			return err
		}
	}

	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Component:     coord.Component,
		Version:       opts.Version,
		CreatedAt:     now().UTC().Truncate(time.Second),
		CreatedBy:     committer(),
		Source:        Source{Commit: commit, Ref: opts.Ref, Repo: repoURL},
		Images:        images(pins),
		Changes:       changes,
		ChangeBasis:   basis,
	}
	m.Notes = RenderNotes(changes, basis)

	result := Result{
		Component: m.Component,
		Version:   m.Version,
		Ref:       ref,
		Tag:       TagName(coord.Component, opts.Version),
		Manifest:  m,
	}

	switch {
	case existing == nil:
		digest, err := Push(ctx, reg, ref, m)
		if err != nil {
			return err
		}
		result.Digest, result.Created = digest, true
		fmt.Fprintf(opts.Stderr, "published %s\n", ref)

	case existing.SameRelease(m):
		// Already published, identically. Report the release that exists
		// rather than the one this run would have built.
		desc, err := reg.Resolve(ctx, ref)
		if err != nil {
			return err
		}
		result.Digest, result.Manifest = desc.Digest, existing
		fmt.Fprintf(opts.Stderr, "%s already published; nothing to do\n", ref)

	default:
		return fmt.Errorf("%s already exists and describes a different release (%s vs %s)\n"+
			"a release is immutable: pick a new version, or delete the tag in the registry if it was published in error",
			ref, shortSHA(existing.Source.Commit), shortSHA(m.Source.Commit))
	}

	// Tag after publishing. The manifest is what a release is; the tag is a
	// convenience for people reading git log, and nothing in this CLI reads it
	// — release discovery goes through the registry. So a tag that cannot be
	// pushed is reported and does not fail the command, and re-running repairs
	// it against the manifest that already exists.
	result.Tagged = tag(ctx, g, result.Tag, commit, m, opts.Stderr)

	return report(opts, result)
}

// lookup reads the manifest already published at ref, if any.
func lookup(ctx context.Context, reg Registry, ref string) (*Manifest, error) {
	m, err := Pull(ctx, reg, ref)
	switch {
	case err == nil:
		return m, nil
	case errors.Is(err, ErrNotFound):
		return nil, nil
	case errors.Is(err, ErrNotRelease):
		// Something else holds this tag. Publishing over it would destroy
		// whatever it is.
		return nil, fmt.Errorf("%s already exists and is not a release manifest: %w", ref, err)
	default:
		return nil, err
	}
}

// resolveBasis decides what the changelog is measured against, and returns the
// commit to start from. An empty from means there is nothing to measure.
func resolveBasis(ctx context.Context, reg Registry, g git.Client, releasesRepo, commit, since, version string) (ChangeBasis, string, error) {
	if since != "" {
		from, err := g.RevParse(ctx, since)
		if err != nil {
			return ChangeBasis{}, "", fmt.Errorf("resolve --since %s: %w", since, err)
		}
		ancestor, err := g.IsAncestor(ctx, from, commit)
		if err != nil {
			return ChangeBasis{}, "", err
		}
		if !ancestor {
			return ChangeBasis{}, "", fmt.Errorf("--since %s is not an ancestor of the commit being released (%s), so there is no range between them", since, shortSHA(commit))
		}
		return ChangeBasis{Kind: BasisExplicit, Ref: since}, from, nil
	}

	prev, err := PreviousRelease(ctx, reg, g, releasesRepo, commit, version)
	if err != nil {
		return ChangeBasis{}, "", err
	}
	if prev == nil {
		// Nothing was released, so there is no prior state to diff against.
		// The window is undefined rather than large, and saying so beats
		// synthesising one: the first managed release of a component is
		// usually adoption, not birth, and --since is how an operator names
		// the point the tool cannot know.
		return ChangeBasis{Kind: BasisInitial}, "", nil
	}
	return ChangeBasis{
		Kind:    BasisRelease,
		Version: prev.Version,
		Commit:  prev.Source.Commit,
	}, prev.Source.Commit, nil
}

// TagName is the annotated tag for a release. Component-prefixed because these
// are monorepos: a bare v1.4.2 would collide the moment a second component in
// the same repository cut a release.
func TagName(component, version string) string {
	return component + "/v" + version
}

// tag creates and pushes the annotated tag, reporting whether it reached the
// remote. Every failure is a warning: the release is already published, and
// the tag is not load-bearing.
func tag(ctx context.Context, g git.Client, name, commit string, m *Manifest, stderr io.Writer) bool {
	at, exists, err := g.TagCommit(ctx, name)
	if err != nil {
		fmt.Fprintf(stderr, "warning: could not read tag %s: %v\n", name, err)
		return false
	}

	switch {
	case exists && at != commit:
		fmt.Fprintf(stderr, "warning: tag %s already points at %s, not %s; leaving it alone\n", name, shortSHA(at), shortSHA(commit))
		return false
	case !exists:
		if err := g.Tag(ctx, name, tagMessage(m), commit); err != nil {
			fmt.Fprintf(stderr, "warning: could not create tag %s: %v\n", name, err)
			return false
		}
	}

	// Pushing a tag the remote already has is a no-op, which is what makes
	// this safe to repeat.
	if err := g.Push(ctx, gitops.RemoteName, "refs/tags/"+name); err != nil {
		fmt.Fprintf(stderr, "warning: release %s is published, but its git tag could not be pushed: %v\n", m.Version, err)
		fmt.Fprintf(stderr, "         the release is valid without it; re-run to repair the tag\n")
		return false
	}
	fmt.Fprintf(stderr, "tagged %s\n", name)
	return true
}

func tagMessage(m *Manifest) string {
	return fmt.Sprintf("%s %s\n\n%s", m.Component, m.Version, m.Notes)
}

func images(pins []source.Pin) []Image {
	out := make([]Image, 0, len(pins))
	for _, p := range pins {
		out = append(out, Image{Name: p.Image, Digest: p.Digest})
	}
	return out
}

func committer() string {
	for _, key := range []string{"GITLAB_USER_EMAIL", "GITHUB_ACTOR", "USER"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func report(opts Options, r Result) error {
	if opts.OutputFormat == OutputJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	return renderCreated(opts.Stdout, r)
}
