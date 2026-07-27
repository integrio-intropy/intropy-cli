package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func TestTagCreatesAnAnnotatedTag(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(ctx, "order-extractor/v1.4.2", "Release 1.4.2", head); err != nil {
		t.Fatal(err)
	}

	// Annotated, not lightweight: the tag exists to be read by a person, and
	// only an annotated tag carries a message.
	if got := gittest.Run(t, dir, "cat-file", "-t", "refs/tags/order-extractor/v1.4.2"); got != "tag" {
		t.Errorf("tag object type = %q, want %q", got, "tag")
	}
	if got := gittest.Run(t, dir, "tag", "-l", "--format=%(contents:subject)", "order-extractor/v1.4.2"); got != "Release 1.4.2" {
		t.Errorf("tag message = %q, want %q", got, "Release 1.4.2")
	}
}

// A release tag that quietly relocated would misrepresent history, so a second
// create at a different commit must fail rather than move the tag.
func TestTagRefusesToMoveAnExistingTag(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	first, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(ctx, "v1.0.0", "one", first); err != nil {
		t.Fatal(err)
	}

	gittest.Commit(t, dir, "component/next.cs", "// next\n", "second")
	second, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(ctx, "v1.0.0", "two", second); err == nil {
		t.Fatal("Tag() over an existing tag should fail")
	}

	got, _, err := g.TagCommit(ctx, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Errorf("tag moved to %s, want it left at %s", ShortSHA(got), ShortSHA(first))
	}
}

// An annotated tag resolves to its own object sha unless dereferenced, so the
// commit must come back rather than the tag object.
func TestTagCommitDereferencesAnnotatedTags(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(ctx, "v1.0.0", "one", head); err != nil {
		t.Fatal(err)
	}

	got, ok, err := g.TagCommit(ctx, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("TagCommit() should report the tag exists")
	}
	if got != head {
		t.Errorf("TagCommit() = %s, want the commit %s", ShortSHA(got), ShortSHA(head))
	}
}

// A missing tag is an answer, not a failure: the caller creates it.
func TestTagCommitMissingTagIsNotAnError(t *testing.T) {
	dir := gittest.NewRepo(t, "main")

	sha, ok, err := testClient(dir).TagCommit(context.Background(), "v9.9.9")
	if err != nil {
		t.Fatalf("TagCommit() on a missing tag should not error: %v", err)
	}
	if ok {
		t.Errorf("TagCommit() reported a tag exists, got sha %q", sha)
	}
}

func TestLogListsCommitsInRange(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	base, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t, dir, "component/a.cs", "// a\n", "Handle empty payloads")
	gittest.Commit(t, dir, "component/b.cs", "// b\n", "Retry on 429")

	got, err := g.Log(ctx, base+"..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Log() returned %d commits, want 2: %+v", len(got), got)
	}
	// Most recent first.
	if got[0].Subject != "Retry on 429" || got[1].Subject != "Handle empty payloads" {
		t.Errorf("Log() subjects = %q, %q", got[0].Subject, got[1].Subject)
	}
	if len(got[0].SHA) != 40 {
		t.Errorf("Log() sha = %q, want a full sha", got[0].SHA)
	}
}

// The changelog is scoped to the component's sourcePaths, so a commit that
// touched only an unrelated directory must not appear in its release notes.
func TestLogScopesToPaths(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	base, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t, dir, filepath.Join("component", "a.cs"), "// a\n", "Handle empty payloads")
	gittest.Commit(t, dir, filepath.Join("unrelated", "b.cs"), "// b\n", "Unrelated change")

	got, err := g.Log(ctx, base+"..HEAD", "component")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Log() returned %d commits, want only the scoped one: %+v", len(got), got)
	}
	if got[0].Subject != "Handle empty payloads" {
		t.Errorf("Log() subject = %q", got[0].Subject)
	}
}

// A subject containing the record delimiter must not split into two commits.
func TestLogHandlesSubjectsWithDelimiters(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	base, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subject := "Fix a|b parsing, and \ttabs"
	gittest.Commit(t, dir, "component/a.cs", "// a\n", subject)

	got, err := g.Log(ctx, base+"..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Log() returned %d commits, want 1: %+v", len(got), got)
	}
	if got[0].Subject != subject {
		t.Errorf("Log() subject = %q, want %q", got[0].Subject, subject)
	}
}

func TestLogEmptyRangeReturnsNothing(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	head, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.Log(ctx, head+"..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Log() over an empty range = %+v, want nothing", got)
	}
}
