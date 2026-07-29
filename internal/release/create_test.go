package release

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// createFixture is everything Create needs: a GitOps repository describing one
// component, a source repository whose HEAD is pushed, a config file pointing
// at the GitOps origin, and an in-memory registry holding the images CI
// "published" for that commit.
type createFixture struct {
	gitopsOrigin string
	sourceDir    string
	sourceOrigin string
	cacheRoot    string
	image        string
	reg          *registry.Client
	registryHost string
}

func newCreateFixture(t *testing.T) *createFixture {
	t.Helper()

	reg, srv := testRegistry(t)
	image := srv.Host + "/integrations/order-extractor"

	gitopsOrigin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:   "orders/order-flow/order-extractor",
		Image:        image,
		Environments: []string{"dev", "prod"},
	})

	sourceOrigin := gittest.NewRepo(t, "main")
	gittest.Commit(t, sourceOrigin, filepath.Join("component", "app.cs"), "// v1\n", "Initial component")

	sourceDir := filepath.Join(t.TempDir(), "src")
	if err := git.Clone(context.Background(), command.ExecRunner{}, sourceOrigin, sourceDir); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, sourceDir, "config", "user.email", "test@example.com")
	gittest.Run(t, sourceDir, "config", "user.name", "Test")
	gittest.Run(t, sourceDir, "config", "commit.gpgsign", "false")

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("INTROPY_GITOPS_REPO", "")
	t.Setenv("GITLAB_USER_EMAIL", "robin.hultman@integrio.se")
	gittest.WriteFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: "+gitopsOrigin+"\n")

	f := &createFixture{
		gitopsOrigin: gitopsOrigin,
		sourceDir:    sourceDir,
		sourceOrigin: sourceOrigin,
		cacheRoot:    t.TempDir(),
		image:        image,
		reg:          reg,
		registryHost: srv.Host,
	}
	f.stubRegistry(t)
	f.publishImage(t)

	// A fixed clock keeps a manifest reproducible across runs.
	original := now
	now = func() time.Time { return time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = original })

	return f
}

// stubRegistry points the package at the in-memory registry.
func (f *createFixture) stubRegistry(t *testing.T) {
	t.Helper()
	original := NewRegistry
	NewRegistry = func(string) (Registry, error) { return f.reg, nil }
	t.Cleanup(func() { NewRegistry = original })
}

// publishImage pushes an image tagged sha-<HEAD>, standing in for CI.
func (f *createFixture) publishImage(t *testing.T) {
	t.Helper()
	head := gittest.HEAD(t, f.sourceDir)
	if _, err := f.reg.PushArtifact(context.Background(), f.image+":sha-"+head, registry.Artifact{
		ArtifactType: "application/vnd.test.image",
		Layers:       []registry.Blob{{MediaType: "application/vnd.oci.image.layer.v1.tar", Data: []byte(head)}},
	}); err != nil {
		t.Fatal(err)
	}
}

// commitAndPush adds a commit to the source and publishes an image for it.
func (f *createFixture) commitAndPush(t *testing.T, path, subject string) {
	t.Helper()
	gittest.Commit(t, f.sourceDir, filepath.Join("component", path), "// "+subject+"\n", subject)
	gittest.Run(t, f.sourceDir, "push", "--quiet", "origin", "main")
	f.publishImage(t)
}

func (f *createFixture) options(version string, stdout, stderr io.Writer) Options {
	return Options{
		Component:    "order-extractor",
		Version:      version,
		SourceDir:    f.sourceDir,
		CacheRoot:    f.cacheRoot,
		OutputFormat: OutputJSON,
		Stdout:       stdout,
		Stderr:       stderr,
	}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return gittest.HEAD(t, dir)
}

// syncBuffer is a bytes.Buffer safe for concurrent writes and reads, so a
// test can watch what a goroutine is writing to stderr.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// create runs Create and decodes the JSON result.
func (f *createFixture) create(t *testing.T, version string) (Result, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Create(context.Background(), f.options(version, &stdout, &stderr)); err != nil {
		t.Fatalf("Create(%s): %v\nstderr: %s", version, err, stderr.String())
	}
	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("decode result: %v\nstdout: %s", err, stdout.String())
	}
	return r, stderr.String()
}

func TestCreatePublishesAManifest(t *testing.T) {
	f := newCreateFixture(t)

	r, _ := f.create(t, "1.0.0")

	if !r.Created {
		t.Error("Created = false on a first publish")
	}
	if r.Digest == "" {
		t.Error("no digest reported")
	}
	if r.Manifest.Component != "order-extractor" || r.Manifest.Version != "1.0.0" {
		t.Errorf("manifest = %+v", r.Manifest)
	}
	if len(r.Manifest.Images) != 1 || !strings.HasPrefix(r.Manifest.Images[0].Digest, "sha256:") {
		t.Errorf("images = %+v, want one pinned digest", r.Manifest.Images)
	}
	if r.Manifest.Source.Commit != gittest.HEAD(t, f.sourceDir) {
		t.Errorf("source.commit = %s, want HEAD", r.Manifest.Source.Commit)
	}
	if r.Manifest.CreatedBy != "robin.hultman@integrio.se" {
		t.Errorf("createdBy = %q", r.Manifest.CreatedBy)
	}

	// It must be readable back as a release.
	got, err := Pull(context.Background(), f.reg, r.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SameRelease(r.Manifest) {
		t.Error("the published manifest differs from the reported one")
	}
}

// The first release has no predecessor, so it says so rather than leaving an
// empty changes[] to be misread as "nothing changed".
func TestFirstReleaseRecordsInitialBasis(t *testing.T) {
	f := newCreateFixture(t)

	r, _ := f.create(t, "1.0.0")

	if r.Manifest.ChangeBasis.Kind != BasisInitial {
		t.Errorf("changeBasis.kind = %q, want %q", r.Manifest.ChangeBasis.Kind, BasisInitial)
	}
	if len(r.Manifest.Changes) != 0 {
		t.Errorf("changes = %+v, want none", r.Manifest.Changes)
	}
	if r.Manifest.Notes != InitialNotes {
		t.Errorf("notes = %q, want %q", r.Manifest.Notes, InitialNotes)
	}
}

func TestSecondReleaseMeasuresFromTheFirst(t *testing.T) {
	f := newCreateFixture(t)
	first, _ := f.create(t, "1.0.0")

	f.commitAndPush(t, "b.cs", "Handle empty payloads")
	second, _ := f.create(t, "1.0.1")

	if second.Manifest.ChangeBasis.Kind != BasisRelease {
		t.Fatalf("changeBasis.kind = %q, want %q", second.Manifest.ChangeBasis.Kind, BasisRelease)
	}
	if second.Manifest.ChangeBasis.Version != "1.0.0" {
		t.Errorf("basis version = %q, want 1.0.0", second.Manifest.ChangeBasis.Version)
	}
	if second.Manifest.ChangeBasis.Commit != first.Manifest.Source.Commit {
		t.Errorf("basis commit = %s, want the first release's commit", second.Manifest.ChangeBasis.Commit)
	}
	if len(second.Manifest.Changes) != 1 || second.Manifest.Changes[0].Subject != "Handle empty payloads" {
		t.Errorf("changes = %+v, want the one new commit", second.Manifest.Changes)
	}
	if !strings.Contains(second.Manifest.Notes, "Handle empty payloads") {
		t.Errorf("notes = %q", second.Manifest.Notes)
	}
}

// A retry after a partial run must recognise its own earlier work rather than
// publishing a second manifest or refusing.
func TestCreateTwiceIsOneManifest(t *testing.T) {
	f := newCreateFixture(t)

	first, _ := f.create(t, "1.0.0")
	second, stderr := f.create(t, "1.0.0")

	if second.Created {
		t.Error("Created = true on a repeat run; the release already existed")
	}
	if second.Digest != first.Digest {
		t.Errorf("digest changed on retry: %s then %s", first.Digest, second.Digest)
	}
	if !strings.Contains(stderr, "already published") {
		t.Errorf("stderr should say the release already existed, got %q", stderr)
	}

	versions, err := ListVersions(context.Background(), f.reg, ReleasesRepo(f.image))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Errorf("registry holds %v, want exactly one version", versions)
	}
}

// The tag is the one thing a retry can repair, so deleting it and re-running
// must put it back without touching the manifest.
func TestCreateRepairsMissingTag(t *testing.T) {
	f := newCreateFixture(t)
	first, _ := f.create(t, "1.0.0")

	tagName := TagName("order-extractor", "1.0.0")
	gittest.Run(t, f.sourceDir, "tag", "-d", tagName)
	gittest.Run(t, f.sourceOrigin, "tag", "-d", tagName)

	second, _ := f.create(t, "1.0.0")

	if second.Created {
		t.Error("the manifest was republished; only the tag needed repair")
	}
	if !second.Tagged {
		t.Error("the tag was not repaired")
	}
	if second.Digest != first.Digest {
		t.Error("the manifest digest changed while repairing a tag")
	}
	if got := gittest.Run(t, f.sourceOrigin, "tag", "-l", tagName); got != tagName {
		t.Errorf("tag %s is still missing from the origin", tagName)
	}
}

// A version is a permanent name for one set of bits. Reusing it for different
// bits must be refused, not silently overwritten.
func TestCreateRefusesDifferentManifestAtSameVersion(t *testing.T) {
	f := newCreateFixture(t)
	f.create(t, "1.0.0")

	f.commitAndPush(t, "b.cs", "Different bits")

	var stdout, stderr bytes.Buffer
	err := Create(context.Background(), f.options("1.0.0", &stdout, &stderr))
	if err == nil {
		t.Fatal("Create() reused a version for a different release")
	}
	for _, want := range []string{"already exists", "immutable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestCreatePushesAnAnnotatedTag(t *testing.T) {
	f := newCreateFixture(t)

	r, _ := f.create(t, "1.4.2")

	tagName := TagName("order-extractor", "1.4.2")
	if r.Tag != tagName {
		t.Errorf("Tag = %q, want %q", r.Tag, tagName)
	}
	if !r.Tagged {
		t.Error("Tagged = false")
	}
	// Component-prefixed: a monorepo holds many components.
	if !strings.HasPrefix(tagName, "order-extractor/") {
		t.Errorf("tag %q should be component-prefixed", tagName)
	}
	if got := gittest.Run(t, f.sourceDir, "cat-file", "-t", "refs/tags/"+tagName); got != "tag" {
		t.Errorf("tag object type = %q, want an annotated tag", got)
	}
	if got := gittest.Run(t, f.sourceOrigin, "tag", "-l", tagName); got != tagName {
		t.Errorf("tag %s did not reach the origin", tagName)
	}
}

// The manifest is what a release is; the tag is for people reading git log,
// and nothing in the CLI reads it — discovery goes through the registry. So a
// tag that cannot be pushed is a warning, not a failure: the release is real,
// and the only cost is a missing convenience.
//
// The origin is given a conflicting tag at another commit, which is what makes
// the push a genuine rejection rather than a broken remote.
func TestCreateSucceedsWhenTheTagCannotBePushed(t *testing.T) {
	f := newCreateFixture(t)
	tagName := TagName("order-extractor", "1.0.0")

	// A different commit already wears this tag on the origin.
	gittest.Commit(t, f.sourceOrigin, filepath.Join("component", "other.cs"), "// other\n", "Unrelated")
	gittest.Run(t, f.sourceOrigin, "tag", "-a", tagName, "-m", "squatting", "HEAD")
	gittest.Run(t, f.sourceOrigin, "reset", "--hard", "--quiet", "HEAD~1")

	var stdout, stderr bytes.Buffer
	if err := Create(context.Background(), f.options("1.0.0", &stdout, &stderr)); err != nil {
		t.Fatalf("a tag that cannot be pushed must not fail the release: %v\nstderr: %s", err, stderr.String())
	}

	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Created {
		t.Error("the manifest should still have been published")
	}
	if r.Tagged {
		t.Error("Tagged = true despite the rejected push")
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr should warn about the tag, got %q", stderr.String())
	}

	// The release itself is intact and readable.
	if _, err := Pull(context.Background(), f.reg, r.Ref); err != nil {
		t.Errorf("the release should be readable despite the tag failure: %v", err)
	}
}

func TestCreateSinceMustBeAnAncestor(t *testing.T) {
	f := newCreateFixture(t)

	// A commit on a branch the release does not contain.
	gittest.Run(t, f.sourceDir, "checkout", "--quiet", "-b", "sidebranch")
	gittest.Commit(t, f.sourceDir, filepath.Join("component", "side.cs"), "// side\n", "Side work")
	side := gittest.HEAD(t, f.sourceDir)
	gittest.Run(t, f.sourceDir, "checkout", "--quiet", "main")

	var stdout, stderr bytes.Buffer
	opts := f.options("1.0.0", &stdout, &stderr)
	opts.Since = side

	err := Create(context.Background(), opts)
	if err == nil {
		t.Fatal("Create() accepted a --since that is not an ancestor")
	}
	if !strings.Contains(err.Error(), "not an ancestor") {
		t.Errorf("error %q should say the ref is not an ancestor", err)
	}
}

// The adoption case: a component deployed by hand for a long time, cut as its
// first managed release, wants notes from a point the tool cannot know.
func TestCreateSinceRecordsAnExplicitBasis(t *testing.T) {
	f := newCreateFixture(t)
	start := gittest.HEAD(t, f.sourceDir)
	f.commitAndPush(t, "b.cs", "Handle empty payloads")

	var stdout, stderr bytes.Buffer
	opts := f.options("1.0.0", &stdout, &stderr)
	opts.Since = start
	if err := Create(context.Background(), opts); err != nil {
		t.Fatalf("%v\nstderr: %s", err, stderr.String())
	}

	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Manifest.ChangeBasis.Kind != BasisExplicit {
		t.Errorf("changeBasis.kind = %q, want %q", r.Manifest.ChangeBasis.Kind, BasisExplicit)
	}
	if r.Manifest.ChangeBasis.Ref != start {
		t.Errorf("changeBasis.ref = %q, want %q", r.Manifest.ChangeBasis.Ref, start)
	}
	if len(r.Manifest.Changes) != 1 {
		t.Errorf("changes = %+v, want the one commit since --since", r.Manifest.Changes)
	}
}

// The whole point of the step: naming the bits moves nothing.
func TestCreateChangesNoEnvironment(t *testing.T) {
	f := newCreateFixture(t)
	before := gittest.HEAD(t, f.gitopsOrigin)

	f.create(t, "1.0.0")

	if after := gittest.HEAD(t, f.gitopsOrigin); after != before {
		t.Errorf("the GitOps repository moved from %s to %s; a release must change no environment", before, after)
	}
}

func TestCreateRequiresAVersion(t *testing.T) {
	f := newCreateFixture(t)

	var stdout, stderr bytes.Buffer
	err := Create(context.Background(), f.options("", &stdout, &stderr))
	if err == nil {
		t.Fatal("Create() without a version should fail")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error %q should mention the version", err)
	}
}

// With --watch, running before CI finished is not a failure: the release
// waits for the image to appear and proceeds from there.
func TestCreateWatchWaitsForTheImage(t *testing.T) {
	f := newCreateFixture(t)

	// A commit whose image CI has not published yet.
	gittest.Commit(t, f.sourceDir, filepath.Join("component", "b.cs"), "// Handle empty payloads\n", "Handle empty payloads")
	gittest.Run(t, f.sourceDir, "push", "--quiet", "origin", "main")

	var stdout bytes.Buffer
	var stderr syncBuffer
	opts := f.options("1.0.0", &stdout, &stderr)
	opts.Watch = true

	done := make(chan error, 1)
	go func() { done <- Create(context.Background(), opts) }()

	// CI finishes only once the release is actually waiting for it: opening
	// the GitOps checkout and inspecting the source take longer than a fixed
	// sleep would reliably cover.
	deadline := time.After(10 * time.Second)
	for !strings.Contains(stderr.String(), "waiting for") {
		select {
		case <-deadline:
			t.Fatalf("the release never started waiting\nstderr: %s", stderr.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	f.publishImage(t)

	if err := <-done; err != nil {
		t.Fatalf("Create() with watch: %v\nstderr: %s", err, stderr.String())
	}

	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("decode result: %v\nstdout: %s", err, stdout.String())
	}
	if !r.Created {
		t.Error("the release should have been created once the image appeared")
	}
	if !strings.Contains(stderr.String(), "waiting for") {
		t.Errorf("stderr should report the wait, got %q", stderr.String())
	}

	// The manifest pinned the digest that appeared.
	if len(r.Manifest.Images) != 1 || !strings.HasPrefix(r.Manifest.Images[0].Digest, "sha256:") {
		t.Errorf("images = %+v, want one pinned digest", r.Manifest.Images)
	}
}
