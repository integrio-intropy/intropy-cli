package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// Summary is one release as a listing sees it.
//
// Field names are stable and additive-only, like the manifest's.
type Summary struct {
	// Version is the registry tag, which is the handle release view takes. It
	// is deliberately the tag rather than the manifest's self-described
	// version: showing a name that does not resolve would be a trap.
	Version string `json:"version"`

	// CreatedAt is nil when the release carries no readable created
	// annotation, rather than a zero time that reads as 1 January year 1.
	CreatedAt *time.Time `json:"createdAt"`

	Commit string `json:"commit"`
	Digest string `json:"digest"`

	// Notes is the first line of the release notes, as recorded in the
	// manifest's description annotation. The full notes are in the manifest,
	// which release view prints.
	Notes string `json:"notes"`
}

// ListResult is the machine-readable outcome of List.
type ListResult struct {
	Component string `json:"component"`

	// Releases is never nil, so a component with no releases marshals as []
	// rather than null.
	Releases []Summary `json:"releases"`

	// Total is how many releases exist, before Limit was applied. A caller
	// reading a limited list can tell it is looking at part of one.
	Total int `json:"total"`
}

// List reports the releases published for a component, newest first.
//
// Like View it changes nothing: it reads the registry, and refreshes the local
// cached GitOps checkout only to learn which registry to ask. It exists because
// release view needs a version, and until now there was no way to discover one.
func List(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	t, err := openTarget(ctx, opts)
	if err != nil {
		return err
	}
	defer t.repo.Close()

	reg, err := NewRegistry(opts.UserAgent)
	if err != nil {
		return err
	}

	releases, err := summarise(ctx, reg, t.releasesRepo, opts.Stderr)
	if err != nil {
		return err
	}

	result := ListResult{Component: t.coord.Component, Releases: releases, Total: len(releases)}
	if opts.Limit > 0 && len(releases) > opts.Limit {
		result.Releases = releases[:opts.Limit]
		fmt.Fprintf(opts.Stderr, "showing %d of %d releases\n", opts.Limit, result.Total)
	}

	if opts.OutputFormat == OutputJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if len(result.Releases) == 0 {
		fmt.Fprintf(opts.Stderr, "%s has no releases yet; publish one with: intropy release create %s --version <version>\n", t.coord, t.coord.Component)
		return nil
	}
	return renderReleases(opts.Stdout, result.Releases)
}

// summarise reads every release in releasesRepo, newest first.
//
// It resolves each tag rather than pulling it: Push mirrors the version, the
// created time, the source commit and the first line of the notes onto the
// manifest as annotations, which is everything a listing shows. A listing of
// forty releases therefore costs forty manifest fetches and no blob transfers.
func summarise(ctx context.Context, reg Registry, releasesRepo string, stderr io.Writer) ([]Summary, error) {
	versions, err := ListVersions(ctx, reg, releasesRepo)
	if err != nil {
		// The repository does not exist yet, so nothing has been released.
		// That is an answer, not a failure.
		if errors.Is(err, ErrNotFound) {
			return []Summary{}, nil
		}
		return nil, fmt.Errorf("list releases in %s: %w", releasesRepo, err)
	}

	releases := make([]Summary, 0, len(versions))
	skipped := 0
	for _, tag := range versions {
		desc, err := reg.Resolve(ctx, Ref(releasesRepo, tag))
		if err != nil {
			// A tag can vanish between the listing and the read, and one
			// unreadable tag must not hide every other release — the same
			// tolerance PreviousRelease applies.
			if errors.Is(err, ErrNotFound) {
				skipped++
				continue
			}
			return nil, fmt.Errorf("read release %s: %w", tag, err)
		}
		// The repository can hold artifacts that are not releases. They are
		// not this command's business, but their absence is worth reporting.
		if desc.ArtifactType != ArtifactType {
			skipped++
			continue
		}
		releases = append(releases, summaryOf(tag, desc))
	}
	switch {
	case skipped == 1:
		fmt.Fprintf(stderr, "skipped 1 tag in %s that is not a readable release\n", releasesRepo)
	case skipped > 1:
		fmt.Fprintf(stderr, "skipped %d tags in %s that are not readable releases\n", skipped, releasesRepo)
	}

	sortNewestFirst(releases)
	return releases, nil
}

// summaryOf reads one release out of its manifest annotations.
func summaryOf(tag string, desc registry.Descriptor) Summary {
	s := Summary{
		Version: tag,
		Commit:  desc.Annotations[AnnotationCommit],
		Digest:  desc.Digest,
		Notes:   desc.Annotations[annotationDescription],
	}
	if created, err := time.Parse(createdLayout, desc.Annotations[annotationCreated]); err == nil {
		s.CreatedAt = &created
	}
	return s
}

// sortNewestFirst orders releases by when they were created.
//
// Publication order is the honest answer to "what is the newest release": a
// version sort would have to guess a versioning scheme, and would put a hotfix
// cut from a maintenance branch above the mainline release that supersedes it.
// Releases with no readable created annotation sort last, and the tag breaks
// ties so the same registry always renders in the same order.
func sortNewestFirst(releases []Summary) {
	slices.SortStableFunc(releases, func(a, b Summary) int {
		switch {
		case a.CreatedAt == nil && b.CreatedAt == nil:
		case a.CreatedAt == nil:
			return 1
		case b.CreatedAt == nil:
			return -1
		case !a.CreatedAt.Equal(*b.CreatedAt):
			return b.CreatedAt.Compare(*a.CreatedAt)
		}
		return strings.Compare(b.Version, a.Version)
	})
}
