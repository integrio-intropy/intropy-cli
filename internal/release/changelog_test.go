//go:build integration

package release

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

func testGit(dir string) git.Client {
	return git.Client{Runner: command.ExecRunner{}, Dir: dir}
}

// publish pushes a release recording commit under version.
func publish(t *testing.T, c *registry.Client, repo, version, commit string) {
	t.Helper()
	m := validManifest()
	m.Version = version
	m.Source.Commit = commit
	m.ChangeBasis = ChangeBasis{Kind: BasisInitial}
	m.Changes = nil
	if _, err := Push(context.Background(), c, Ref(repo, version), m); err != nil {
		t.Fatalf("publish %s: %v", version, err)
	}
}

func TestChangelogListsCommitsSincePredecessor(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()

	base, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t, dir, filepath.Join("component", "a.cs"), "// a\n", "Handle empty payloads")
	gittest.Commit(t, dir, filepath.Join("component", "b.cs"), "// b\n", "Retry on 429")
	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Changelog(ctx, g, base, head, []string{"component"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Changelog() = %+v, want 2 changes", got)
	}
	if got[0].Subject != "Retry on 429" {
		t.Errorf("first change = %q, want the most recent commit", got[0].Subject)
	}
}

// The predecessor's own commit is already released, so it is not part of what
// changed.
func TestChangelogExcludesThePredecessorCommit(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()

	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Changelog(ctx, g, head, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Changelog() over an already-released commit = %+v, want nothing", got)
	}
}

func TestPreviousReleaseNeverReleased(t *testing.T) {
	c, srv := testRegistry(t)
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()

	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := PreviousRelease(ctx, c, g, ReleasesRepo(srv.Host+"/order-extractor"), head, "")
	if err != nil {
		t.Fatalf("a component with no releases is not an error: %v", err)
	}
	if got != nil {
		t.Errorf("PreviousRelease() = %+v, want nil", got)
	}
}

func TestPreviousReleasePicksTheNearestAncestor(t *testing.T) {
	c, srv := testRegistry(t)
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()
	repo := ReleasesRepo(srv.Host + "/order-extractor")

	first, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, c, repo, "1.4.0", first)

	gittest.Commit(t, dir, "component/a.cs", "// a\n", "one")
	second, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, c, repo, "1.4.1", second)

	gittest.Commit(t, dir, "component/b.cs", "// b\n", "two")
	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := PreviousRelease(ctx, c, g, repo, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Version != "1.4.1" {
		t.Fatalf("PreviousRelease() = %+v, want 1.4.1", got)
	}
}

// The case that justifies ancestry over any form of version sorting.
//
// History branches at 1.0.0. main goes on to release 1.1.0; a parallel branch
// off the same point is about to release 1.2.0. Only 1.0.0 is an ancestor of
// that branch, so it is the only honest basis.
//
// This fixture is built to defeat the two plausible shortcuts. "The most
// recent release" picks 1.1.0, because it was published last. "The highest
// version below the one being released" also picks 1.1.0, because 1.1.0 < 1.2.0.
// Both would report "Only on main" as part of a release that does not contain
// that commit.
func TestPreviousReleaseIgnoresReleasesOnOtherBranches(t *testing.T) {
	c, srv := testRegistry(t)
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()
	repo := ReleasesRepo(srv.Host + "/order-extractor")

	fork, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, c, repo, "1.0.0", fork)

	// main moves on and releases 1.1.0 — the most recently published release.
	gittest.Commit(t, dir, "component/b.cs", "// b\n", "Only on main")
	mainline, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, c, repo, "1.1.0", mainline)

	// The parallel branch forks from 1.0.0, never seeing main's commit, and is
	// about to be released as 1.2.0 — a higher version than 1.1.0.
	gittest.Run(t, dir, "checkout", "--quiet", "-b", "parallel", fork)
	gittest.Commit(t, dir, "component/c.cs", "// c\n", "Fix the thing")
	parallel, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := PreviousRelease(ctx, c, g, repo, parallel, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("PreviousRelease() = nil, want 1.0.0")
	}
	if got.Version != "1.0.0" {
		t.Fatalf("PreviousRelease() = %s, want 1.0.0 — %s is not an ancestor of the commit being released", got.Version, got.Version)
	}

	changes, err := Changelog(ctx, g, got.Source.Commit, parallel, []string{"component"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.Subject == "Only on main" {
			t.Error("changelog contains a commit the release does not contain")
		}
	}
	if len(changes) != 1 || changes[0].Subject != "Fix the thing" {
		t.Errorf("changes = %+v, want only the parallel branch's commit", changes)
	}
}

// An unreadable tag must not make every later release unchangeloggable.
func TestPreviousReleaseSkipsUnreadableTags(t *testing.T) {
	c, srv := testRegistry(t)
	dir := gittest.NewRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()
	repo := ReleasesRepo(srv.Host + "/order-extractor")

	first, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, c, repo, "1.4.0", first)

	// Something that is not a release, sharing the repository.
	if _, err := c.PushArtifact(ctx, Ref(repo, "junk"), registry.Artifact{
		ArtifactType: "application/vnd.something.else",
		Layers:       []registry.Blob{{MediaType: "text/plain", Data: []byte("junk")}},
	}); err != nil {
		t.Fatal(err)
	}

	gittest.Commit(t, dir, "component/a.cs", "// a\n", "one")
	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := PreviousRelease(ctx, c, g, repo, head, "")
	if err != nil {
		t.Fatalf("an unreadable neighbour must not fail discovery: %v", err)
	}
	if got == nil || got.Version != "1.4.0" {
		t.Errorf("PreviousRelease() = %+v, want 1.4.0", got)
	}
}

func TestRenderNotes(t *testing.T) {
	changes := []Change{
		{Subject: "Handle empty payloads"},
		{Subject: "Retry on 429"},
	}
	got := RenderNotes(changes, ChangeBasis{Kind: BasisRelease})
	want := "- Handle empty payloads\n- Retry on 429\n"
	if got != want {
		t.Errorf("RenderNotes() = %q, want %q", got, want)
	}
}

// A blank note would leave a reader guessing why there is nothing there.
func TestRenderNotesSaysInitialRelease(t *testing.T) {
	got := RenderNotes(nil, ChangeBasis{Kind: BasisInitial})
	if got != InitialNotes {
		t.Errorf("RenderNotes() = %q, want %q", got, InitialNotes)
	}
	if !strings.Contains(got, "Initial") {
		t.Errorf("notes %q should say the release is the first", got)
	}
}
