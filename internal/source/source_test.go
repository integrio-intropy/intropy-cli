//go:build integration

package source

import (
	"context"
	"errors"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"path/filepath"
	"strings"
	"testing"
)

// sourceGit builds a client rooted at a source repository.
func sourceGit(dir string) git.Client {
	return git.Client{Runner: command.ExecRunner{}, Dir: dir}
}

// newSourceClone creates an origin repository and a clone of it, returning the
// clone. Source checks reason about the remote, so a clone with a real origin is
// the minimum honest fixture.
func newSourceClone(t *testing.T) (clone, origin string) {
	t.Helper()
	origin = gittest.NewRepo(t, "main")
	clone = filepath.Join(t.TempDir(), "src")
	if err := git.Clone(context.Background(), command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	return clone, origin
}

func TestInspectCleanAndPushed(t *testing.T) {
	clone, _ := newSourceClone(t)

	st, err := Inspect(context.Background(), sourceGit(clone), []string{"component"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Commit) != 40 {
		t.Errorf("Commit = %q, want a full sha", st.Commit)
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main", st.Branch)
	}
	if len(st.Dirty) != 0 {
		t.Errorf("Dirty = %v, want none", st.Dirty)
	}
	if st.ShortCommit() != st.Commit[:7] {
		t.Errorf("ShortCommit() = %q", st.ShortCommit())
	}
}

func TestInspectRefusesDirtyComponent(t *testing.T) {
	clone, _ := newSourceClone(t)
	gittest.WriteFile(t, filepath.Join(clone, "component", "app.cs"), "// uncommitted\n")

	_, err := Inspect(context.Background(), sourceGit(clone), []string{"component"}, false)
	if err == nil {
		t.Fatal("expected a dirty-worktree error")
	}
	if _, ok := errors.AsType[*DirtyWorktreeError](err); !ok {
		t.Fatalf("error %v should be *DirtyWorktreeError", err)
	}
	// The message must name the files and say why this matters, otherwise it
	// reads as pedantry and people reach for --allow-dirty reflexively.
	for _, want := range []string{"component/app.cs", "CI builds the pushed commit", "--allow-dirty"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// The reason the check is scoped: these are monorepos holding many components,
// and an unrelated dirty file is no reason to refuse this component's deploy.
func TestInspectIgnoresDirtFilesOutsideSourcePaths(t *testing.T) {
	clone, _ := newSourceClone(t)
	gittest.WriteFile(t, filepath.Join(clone, "other-component", "app.cs"), "// someone else's work\n")
	gittest.WriteFile(t, filepath.Join(clone, "scratch.txt"), "notes\n")

	st, err := Inspect(context.Background(), sourceGit(clone), []string{"component"}, false)
	if err != nil {
		t.Fatalf("dirt outside sourcePaths should not block a deploy: %v", err)
	}
	if len(st.Dirty) != 0 {
		t.Errorf("Dirty = %v, want none for this component", st.Dirty)
	}
}

func TestInspectAllowDirtyStillReports(t *testing.T) {
	clone, _ := newSourceClone(t)
	gittest.WriteFile(t, filepath.Join(clone, "component", "app.cs"), "// uncommitted\n")

	st, err := Inspect(context.Background(), sourceGit(clone), []string{"component"}, true)
	if err != nil {
		t.Fatalf("--allow-dirty should waive the check: %v", err)
	}
	// Waived, not hidden: the caller warns with this.
	if len(st.Dirty) != 1 {
		t.Errorf("Dirty = %v, want the change still reported", st.Dirty)
	}
}

func TestInspectRefusesUnpushedCommit(t *testing.T) {
	clone, _ := newSourceClone(t)
	gittest.Commit(t, clone, "component/app.cs", "// local only\n", "local work")

	_, err := Inspect(context.Background(), sourceGit(clone), []string{"component"}, false)
	if err == nil {
		t.Fatal("expected an unpushed-commit error")
	}
	if _, ok := errors.AsType[*UnpushedCommitError](err); !ok {
		t.Fatalf("error %v should be *UnpushedCommitError", err)
	}
	if !strings.Contains(err.Error(), "no image built from it") {
		t.Errorf("error %q should explain why an unpushed commit cannot be deployed", err)
	}
}

// A stale remote-tracking ref would make a commit that was pushed look
// unpushed — the most confusing possible version of this error. Inspect
// fetches first, so a stale origin/main must not produce a false negative.
func TestInspectFetchesBeforeCheckingAncestry(t *testing.T) {
	clone, _ := newSourceClone(t)
	g := sourceGit(clone)
	ctx := context.Background()

	stale, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t, clone, "component/app.cs", "// pushed\n", "pushed work")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")

	// Rewind the remote-tracking ref to simulate a clone that has not fetched
	// since the push.
	gittest.Run(t, clone, "update-ref", "refs/remotes/origin/main", stale)

	st, err := Inspect(ctx, g, []string{"component"}, false)
	if err != nil {
		t.Fatalf("a stale origin/main must not report a pushed commit as unpushed: %v", err)
	}
	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != head {
		t.Errorf("Commit = %q, want %q", st.Commit, head)
	}
}

// Passing no source paths falls back to the whole tree rather than silently
// checking nothing, which would defeat the purpose of the check.
func TestInspectNoSourcePathsChecksWholeTree(t *testing.T) {
	clone, _ := newSourceClone(t)
	gittest.WriteFile(t, filepath.Join(clone, "anywhere.txt"), "dirty\n")

	_, err := Inspect(context.Background(), sourceGit(clone), nil, false)
	if err == nil {
		t.Fatal("with no source paths the whole tree should be checked")
	}
	if !strings.Contains(err.Error(), "the working tree") {
		t.Errorf("error %q should describe the scope as the whole working tree", err)
	}
}
