package release

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/git"
)

// InitialNotes is what a release with no basis for comparison says, rather
// than leaving the notes blank and letting a reader guess.
const InitialNotes = "Initial release."

// Ancestry answers whether one commit descends from another. It is the seam
// that lets predecessor selection be tested without a real repository, and is
// satisfied by git.Client.
type Ancestry interface {
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// PreviousRelease finds the release that commit most recently descends from.
//
// The registry is the authority for which releases exist — a git tag that
// failed to push must not hide one — but "which release does this commit
// descend from" is a lineage question, so ancestry decides between the
// candidates rather than a version sort. Sorting by version breaks the moment
// history branches: a hotfix cut from a maintenance branch would take the
// newer release on the mainline as its predecessor and report a changelog full
// of commits it does not contain.
//
// Releases the commit does not descend from are skipped. Of those it does, the
// nearest wins — the one every other candidate is an ancestor of.
//
// The version being released is excluded from the candidates. Re-running
// create for a version that already exists must reproduce the manifest it
// published the first time, and a release whose own commit is an ancestor of
// itself — git counts equal commits as ancestors — would otherwise become its
// own predecessor and change the basis on every retry.
//
// A nil manifest with a nil error means there is no predecessor: either
// nothing has been released, or nothing released is an ancestor.
func PreviousRelease(ctx context.Context, reg Registry, anc Ancestry, releasesRepo, commit, excludeVersion string) (*Manifest, error) {
	versions, err := ListVersions(ctx, reg, releasesRepo)
	if err != nil {
		// Never released: the repository does not exist yet.
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("list releases in %s: %w", releasesRepo, err)
	}

	var candidates []*Manifest
	for _, v := range versions {
		if v == excludeVersion {
			continue
		}
		m, err := Pull(ctx, reg, Ref(releasesRepo, v))
		if err != nil {
			// One unreadable tag must not make every later release
			// unchangeloggable. Skip it; the basis simply reaches further back.
			if errors.Is(err, ErrNotRelease) || errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("read release %s: %w", v, err)
		}

		descends, err := anc.IsAncestor(ctx, m.Source.Commit, commit)
		if err != nil {
			// A release built from a commit this clone has never seen is not
			// an ancestor as far as we can tell, and not a reason to fail.
			continue
		}
		if descends {
			candidates = append(candidates, m)
		}
	}

	return nearest(ctx, anc, candidates)
}

// nearest returns the candidate that every other candidate is an ancestor of.
func nearest(ctx context.Context, anc Ancestry, candidates []*Manifest) (*Manifest, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		// c is nearer when best is an ancestor of it.
		nearer, err := anc.IsAncestor(ctx, best.Source.Commit, c.Source.Commit)
		if err != nil {
			return nil, err
		}
		if nearer {
			best = c
		}
	}
	return best, nil
}

// Changelog lists the commits in from..to that touched sourcePaths.
//
// from is exclusive and to is inclusive, matching git's range syntax: the
// predecessor's own commit is already released, so it is not part of what
// changed.
func Changelog(ctx context.Context, g git.Client, from, to string, sourcePaths []string) ([]Change, error) {
	commits, err := g.Log(ctx, from+".."+to, sourcePaths...)
	if err != nil {
		return nil, err
	}

	changes := make([]Change, 0, len(commits))
	for _, c := range commits {
		changes = append(changes, Change{Commit: c.SHA, Subject: c.Subject})
	}
	return changes, nil
}

// RenderNotes turns the changelog into the manifest's notes.
func RenderNotes(changes []Change, basis ChangeBasis) string {
	if basis.Kind == BasisInitial && len(changes) == 0 {
		return InitialNotes
	}
	if len(changes) == 0 {
		return ""
	}

	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "- %s\n", c.Subject)
	}
	return b.String()
}
