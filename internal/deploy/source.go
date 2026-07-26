package deploy

import (
	"context"
	"fmt"
	"strings"
)

// SourceState is what the source repository says about the commit being
// deployed.
type SourceState struct {
	// Commit is the full sha of HEAD.
	Commit string

	// Branch is the source remote's default branch, the one CI builds.
	Branch string

	// Dirty lists uncommitted changes under the component's source paths. It
	// is populated even when the check is waived, so callers can warn.
	Dirty []string
}

// ShortCommit abbreviates the commit for messages and commit subjects.
func (s SourceState) ShortCommit() string { return short(s.Commit) }

// InspectSource reads the state of the source repository at dir and checks
// that its HEAD is a safe thing to deploy.
//
// Two conditions are enforced. The working tree must be clean under the
// component's source paths, because CI built the pushed commit and uncommitted
// changes mean the thing about to be deployed is not the thing that was just
// tested. And HEAD must be an ancestor of the remote's default branch, because
// an unpushed commit has no image in the registry at all.
//
// The cleanliness check is scoped to sourcePaths rather than applied to the
// whole tree: these are monorepos holding many components, and an unrelated
// dirty file elsewhere is no reason to refuse this deploy. allowDirty waives
// the check but not the reporting.
func InspectSource(ctx context.Context, g Git, sourcePaths []string, allowDirty bool) (SourceState, error) {
	var st SourceState

	commit, err := g.HEAD(ctx)
	if err != nil {
		return st, err
	}
	st.Commit = commit

	dirty, err := g.Status(ctx, sourcePaths...)
	if err != nil {
		return st, err
	}
	st.Dirty = dirty
	if len(dirty) > 0 && !allowDirty {
		return st, &DirtyWorktreeError{Paths: sourcePaths, Changes: dirty}
	}

	branch, err := g.DefaultBranch(ctx, remoteName)
	if err != nil {
		return st, err
	}
	st.Branch = branch

	// Fetch before reasoning about ancestry. A stale remote-tracking ref makes
	// a commit that was pushed yesterday look unpushed, which is the most
	// confusing possible version of this error.
	if err := g.Fetch(ctx, remoteName, branch); err != nil {
		return st, err
	}
	remoteRef := remoteName + "/" + branch
	pushed, err := g.IsAncestor(ctx, commit, remoteRef)
	if err != nil {
		return st, err
	}
	if !pushed {
		return st, &UnpushedCommitError{Commit: commit, Branch: remoteRef}
	}

	return st, nil
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
	b.WriteString("\nCI builds the pushed commit, so deploying now would ship something other than what you just tested.")
	b.WriteString("\nCommit and push, or pass --allow-dirty if you are certain the image matches.")
	return b.String()
}

// UnpushedCommitError reports that HEAD has not reached the remote.
type UnpushedCommitError struct {
	Commit string
	Branch string
}

func (e *UnpushedCommitError) Error() string {
	return fmt.Sprintf("HEAD (%s) is not an ancestor of %s, so it has not been pushed; there is no image built from it yet — push first", short(e.Commit), e.Branch)
}
