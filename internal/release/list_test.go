package release

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// createAt publishes a release at a given day in July 2026. newCreateFixture
// freezes the clock, which would leave every release with the same created
// annotation and make ordering untestable.
func (f *createFixture) createAt(t *testing.T, version string, day int) Result {
	t.Helper()
	original := now
	now = func() time.Time { return time.Date(2026, 7, day, 10, 0, 0, 0, time.UTC) }
	defer func() { now = original }()

	r, _ := f.create(t, version)
	return r
}

func (f *createFixture) list(t *testing.T, format string, limit int) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = List(context.Background(), Options{
		Component:    "order-extractor",
		CacheRoot:    f.cacheRoot,
		OutputFormat: format,
		Limit:        limit,
		Stdout:       &out,
		Stderr:       &errOut,
	})
	return out.String(), errOut.String(), err
}

// threeReleases publishes 1.0.0, 1.1.0 and 2.0.0 in that order, on separate days.
func (f *createFixture) threeReleases(t *testing.T) {
	t.Helper()
	f.createAt(t, "1.0.0", 20)
	f.commitAndPush(t, "b.cs", "Handle empty payloads")
	f.createAt(t, "1.1.0", 22)
	f.commitAndPush(t, "c.cs", "Retry on timeout")
	f.createAt(t, "2.0.0", 24)
}

func (f *createFixture) listJSON(t *testing.T, limit int) (ListResult, string) {
	t.Helper()
	stdout, stderr, err := f.list(t, OutputJSON, limit)
	if err != nil {
		t.Fatalf("List: %v\nstderr: %s", err, stderr)
	}
	var got ListResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("list output is not a ListResult: %v\n%s", err, stdout)
	}
	return got, stderr
}

// Publication order, not version order: the newest release is the one most
// recently cut, and that is what a reader is looking for at the top.
func TestListReportsReleasesNewestFirst(t *testing.T) {
	f := newCreateFixture(t)
	f.threeReleases(t)

	stdout, _, err := f.list(t, OutputPlain, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout, "VERSION") || !strings.Contains(stdout, "CREATED") {
		t.Errorf("output should have a header row:\n%s", stdout)
	}
	newest, middle, oldest := strings.Index(stdout, "2.0.0"), strings.Index(stdout, "1.1.0"), strings.Index(stdout, "1.0.0")
	if newest < 0 || middle < 0 || oldest < 0 {
		t.Fatalf("output should list every release:\n%s", stdout)
	}
	if !(newest < middle && middle < oldest) {
		t.Errorf("releases should be newest first:\n%s", stdout)
	}
	for _, want := range []string{"2026-07-24", "2026-07-20", "Retry on timeout"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output should contain %q:\n%s", want, stdout)
		}
	}
}

func TestListJSONDescribesEveryRelease(t *testing.T) {
	f := newCreateFixture(t)
	f.createAt(t, "1.0.0", 20)
	f.commitAndPush(t, "b.cs", "Handle empty payloads")
	latest := f.createAt(t, "1.1.0", 22)

	got, _ := f.listJSON(t, 0)

	if got.Component != "order-extractor" {
		t.Errorf("component = %q", got.Component)
	}
	if got.Total != 2 || len(got.Releases) != 2 {
		t.Fatalf("got %d of %d releases, want 2 of 2: %+v", len(got.Releases), got.Total, got.Releases)
	}

	first := got.Releases[0]
	if first.Version != "1.1.0" {
		t.Errorf("releases[0].version = %q, want the newest", first.Version)
	}
	if first.Commit != latest.Manifest.Source.Commit {
		t.Errorf("releases[0].commit = %q, want %q", first.Commit, latest.Manifest.Source.Commit)
	}
	if first.Digest != latest.Digest {
		t.Errorf("releases[0].digest = %q, want %q", first.Digest, latest.Digest)
	}
	if first.CreatedAt == nil || !first.CreatedAt.Equal(time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("releases[0].createdAt = %v", first.CreatedAt)
	}
	// The first line of the notes, so a listing says what changed.
	if first.Notes != "Handle empty payloads" {
		t.Errorf("releases[0].notes = %q", first.Notes)
	}
}

// The version in a listing is the handle release view takes.
func TestListVersionsCanBeViewed(t *testing.T) {
	f := newCreateFixture(t)
	f.createAt(t, "1.0.0", 20)

	got, _ := f.listJSON(t, 0)
	if len(got.Releases) != 1 {
		t.Fatalf("releases = %+v, want one", got.Releases)
	}
	if _, err := f.view(t, got.Releases[0].Version, OutputPlain); err != nil {
		t.Errorf("the listed version should be viewable: %v", err)
	}
}

// A component that has never been released has an answer, not a failure: the
// releases repository does not exist yet.
func TestListNeverReleasedIsNotAnError(t *testing.T) {
	f := newCreateFixture(t)

	stdout, stderr, err := f.list(t, OutputPlain, 0)
	if err != nil {
		t.Fatalf("listing a component with no releases must not fail: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	if !strings.Contains(stderr, "no releases") {
		t.Errorf("stderr should say there are no releases yet, got %q", stderr)
	}

	// JSON must stay parseable, with an empty list rather than null.
	got, _ := f.listJSON(t, 0)
	if got.Releases == nil {
		t.Error("releases should marshal as [], not null")
	}
	if got.Total != 0 {
		t.Errorf("total = %d, want 0", got.Total)
	}
}

// A limit that hides releases says so. A truncated list that looks complete
// would be read as the whole history.
func TestListLimitReportsWhatItHides(t *testing.T) {
	f := newCreateFixture(t)
	f.threeReleases(t)

	got, stderr := f.listJSON(t, 2)

	if len(got.Releases) != 2 {
		t.Errorf("got %d releases, want the 2 newest", len(got.Releases))
	}
	if got.Total != 3 {
		t.Errorf("total = %d, want 3 — the count before the limit", got.Total)
	}
	if got.Releases[0].Version != "2.0.0" || got.Releases[1].Version != "1.1.0" {
		t.Errorf("kept %+v, want the two newest", got.Releases)
	}
	for _, want := range []string{"2 of 3", "--limit 0"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr should mention %q, got %q", want, stderr)
		}
	}
}

// A limit at or above the total hides nothing and says nothing.
func TestListLimitIsSilentWhenNothingIsHidden(t *testing.T) {
	f := newCreateFixture(t)
	f.createAt(t, "1.0.0", 20)

	_, stderr := f.listJSON(t, 20)

	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// The releases repository can hold artifacts that are not releases. They are
// skipped rather than failing the listing, and the skip is reported.
func TestListSkipsArtifactsThatAreNotReleases(t *testing.T) {
	f := newCreateFixture(t)
	f.createAt(t, "1.0.0", 20)

	if _, err := f.reg.PushArtifact(context.Background(), Ref(ReleasesRepo(f.image), "not-a-release"), registry.Artifact{
		ArtifactType: "application/vnd.test.other",
		Layers:       []registry.Blob{{MediaType: "application/vnd.oci.image.layer.v1.tar", Data: []byte("junk")}},
	}); err != nil {
		t.Fatal(err)
	}

	got, stderr := f.listJSON(t, 0)

	if got.Total != 1 || len(got.Releases) != 1 || got.Releases[0].Version != "1.0.0" {
		t.Errorf("got %+v, want only the real release", got.Releases)
	}
	if !strings.Contains(stderr, "skipped 1 tag") {
		t.Errorf("stderr should report the skipped tag, got %q", stderr)
	}
}

// Listing is a read. It refreshes the local GitOps cache and nothing else.
func TestListChangesNothing(t *testing.T) {
	f := newCreateFixture(t)
	f.createAt(t, "1.0.0", 20)
	gitopsBefore := headOf(t, f.gitopsOrigin)
	sourceBefore := headOf(t, f.sourceDir)

	if _, _, err := f.list(t, OutputPlain, 0); err != nil {
		t.Fatal(err)
	}

	if got := headOf(t, f.gitopsOrigin); got != gitopsBefore {
		t.Error("list moved the GitOps repository")
	}
	if got := headOf(t, f.sourceDir); got != sourceBefore {
		t.Error("list moved the source repository")
	}
}

// A release with no readable created annotation still has to appear, and in a
// stable place: last, with the tag breaking ties.
func TestSortNewestFirstPutsUndatedReleasesLast(t *testing.T) {
	at := func(day int) *time.Time {
		d := time.Date(2026, 7, day, 10, 0, 0, 0, time.UTC)
		return &d
	}
	releases := []Summary{
		{Version: "1.0.0", CreatedAt: at(20)},
		{Version: "0.9.0"},
		{Version: "2.0.0", CreatedAt: at(24)},
		{Version: "1.5.0"},
	}

	sortNewestFirst(releases)

	var got []string
	for _, r := range releases {
		got = append(got, r.Version)
	}
	want := []string{"2.0.0", "1.0.0", "1.5.0", "0.9.0"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
