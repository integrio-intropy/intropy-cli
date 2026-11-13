// Package source reads the state of a component's source repository: which
// commit is being shipped, whether it is safe to ship, and which image digests
// CI published for it.
//
// It is shared by the commands that need to name a specific build — deploy
// pins those digests into an overlay, release records them in a manifest — so
// it sits below both rather than inside either.
package source

import (
	"context"
	"fmt"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// State is what the source repository says about the commit being shipped.
type State struct {
	// Commit is the full sha of the commit being shipped.
	Commit string

	// Head is the full sha of HEAD. It differs from Commit when the commit was
	// named explicitly, or when newer commits touch none of the source paths.
	Head string

	// Ref is the revision the caller named, empty when the commit was selected
	// from HEAD.
	Ref string

	// Skipped counts the first-parent commits between Commit and HEAD that
	// touch none of the source paths. Only set when Ref is empty.
	Skipped int

	// Branch is the source remote's default branch, the one CI builds.
	Branch string

	// Dirty lists uncommitted changes under the component's source paths. It
	// is populated even when the check is waived, so callers can warn. Empty
	// when Ref is set: a named commit does not involve the working tree.
	Dirty []string
}

// ShortCommit abbreviates the commit for messages and commit subjects.
func (s State) ShortCommit() string { return git.ShortSHA(s.Commit) }

// Selection describes, for a progress line, how Commit was chosen when that is
// not simply HEAD. Empty when there is nothing to say.
func (s State) Selection() string {
	switch {
	case s.Ref != "":
		return fmt.Sprintf("using %s (%s)", s.ShortCommit(), s.Ref)
	case s.Skipped > 0:
		return fmt.Sprintf("using %s, the newest commit under the component's source paths; HEAD (%s) is %d commit(s) ahead and touches none of them",
			s.ShortCommit(), git.ShortSHA(s.Head), s.Skipped)
	default:
		return ""
	}
}

// Inspect decides which commit of the source repository at dir is being
// shipped, and checks that it is a safe thing to ship.
//
// With ref set, that revision is shipped as named. With ref empty the commit is
// the newest one on HEAD that touched sourcePaths, not HEAD itself: these are
// monorepos, and a teammate's push to another component between your push and
// your release must not change what your release means. CI may not have built
// this component for that commit at all.
//
// The shipped commit must be an ancestor of the remote's default branch,
// because an unpushed commit has no image in the registry. When ref is empty
// the working tree must also be clean under sourcePaths, because uncommitted
// changes mean the thing about to be shipped is not the thing that was just
// tested; allowDirty waives that check but not the reporting. A named ref
// skips it: the working tree plays no part in what it means.
//
// The cleanliness check is scoped to sourcePaths rather than applied to the
// whole tree, so an unrelated dirty file elsewhere is no reason to refuse.
func Inspect(ctx context.Context, g git.Client, sourcePaths []string, ref string, allowDirty bool) (State, error) {
	st := State{Ref: ref}

	head, err := g.HEAD(ctx)
	if err != nil {
		return st, err
	}
	st.Head = head

	if ref == "" {
		dirty, err := g.Status(ctx, sourcePaths...)
		if err != nil {
			return st, err
		}
		st.Dirty = dirty
		if len(dirty) > 0 && !allowDirty {
			return st, &DirtyWorktreeError{Paths: sourcePaths, Changes: dirty}
		}
	}

	branch, err := g.DefaultBranch(ctx, gitops.RemoteName)
	if err != nil {
		return st, err
	}
	st.Branch = branch

	// Fetch before resolving or reasoning about ancestry. A stale
	// remote-tracking ref makes a commit that was pushed yesterday look
	// unpushed, which is the most confusing possible version of this error,
	// and leaves a ref such as origin/main pointing at the wrong commit.
	if err := g.Fetch(ctx, gitops.RemoteName, branch); err != nil {
		return st, err
	}

	if ref != "" {
		// ^{commit} so an annotated tag resolves to the commit it names, not
		// to the tag object, which no image is tagged with.
		if st.Commit, err = g.RevParse(ctx, ref+"^{commit}"); err != nil {
			return st, err
		}
	} else if st.Commit, st.Skipped, err = lastSourceChange(ctx, g, head, sourcePaths); err != nil {
		return st, err
	}

	remoteRef := gitops.RemoteName + "/" + branch
	pushed, err := g.IsAncestor(ctx, st.Commit, remoteRef)
	if err != nil {
		return st, err
	}
	if !pushed {
		return st, &UnpushedCommitError{Commit: st.Commit, Branch: remoteRef}
	}

	return st, nil
}

// lastSourceChange returns the newest commit on head that touched sourcePaths
// and how many commits head is ahead of it. With no source paths, or none that
// any commit touched, it is head itself: there is nothing to narrow by.
func lastSourceChange(ctx context.Context, g git.Client, head string, sourcePaths []string) (string, int, error) {
	if len(sourcePaths) == 0 {
		return head, 0, nil
	}
	commit, found, err := g.LastChange(ctx, head, sourcePaths...)
	if err != nil || !found || commit == head {
		return head, 0, err
	}
	skipped, err := g.CountFirstParent(ctx, commit+".."+head)
	if err != nil {
		return "", 0, err
	}
	return commit, skipped, nil
}

// DirtyWorktreeError reports uncommitted changes in the component's source.
type DirtyWorktreeError struct {
	Paths   []string
	Changes []string
}

func (e *DirtyWorktreeError) Error() string {
	var b strings.Builder
	scope := "the working tree"
	if len(e.Paths) > 0 {
		scope = strings.Join(e.Paths, ", ")
	}
	fmt.Fprintf(&b, "uncommitted changes under %s:", scope)
	for _, c := range e.Changes {
		fmt.Fprintf(&b, "\n  %s", c)
	}
	b.WriteString("\nCI builds the pushed commit, so this would ship something other than what you just tested.")
	b.WriteString("\nCommit and push, or pass --allow-dirty if you are certain the image matches.")
	return b.String()
}

// UnpushedCommitError reports that the commit being shipped has not reached
// the remote.
type UnpushedCommitError struct {
	Commit string
	Branch string
}

func (e *UnpushedCommitError) Error() string {
	return fmt.Sprintf("%s is not an ancestor of %s, so it has not been pushed; there is no image built from it yet — push first", git.ShortSHA(e.Commit), e.Branch)
}
