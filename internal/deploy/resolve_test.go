package deploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/registry"
	"github.com/integrio-intropy/intropy-cli/internal/registry/registrytest"
	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/integrio-intropy/intropy-cli/internal/source"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const (
	testVersion       = "1.4.2"
	testReleaseCommit = "197a3ae981068c375be77cb03e8c85e5ce304612"
)

// releaseFixture is a runFixture whose images live on an in-memory registry, so
// a release manifest can be published for them.
type releaseFixture struct {
	runFixture
	reg release.Registry
}

// newReleaseFixture hosts the component's images on an in-memory registry and
// points release.NewRegistry at it. A release deploy needs that; it needs no
// source repository at all.
func newReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()

	srv := registrytest.NewServer()
	t.Cleanup(srv.Close)
	reg, err := registry.NewClient(
		registry.WithCredentials(func(context.Context, string) (auth.Credential, error) {
			return auth.EmptyCredential, nil
		}),
		registry.WithHTTPClient(srv.Client()),
		registry.WithPlainHTTP(func(string) bool { return true }),
	)
	if err != nil {
		t.Fatal(err)
	}

	f := newRunFixtureWithImage(t, srv.Host+"/integrations/order-extractor")

	original := release.NewRegistry
	release.NewRegistry = func(string) (release.Registry, error) { return reg, nil }
	t.Cleanup(func() { release.NewRegistry = original })

	return releaseFixture{runFixture: f, reg: reg}
}

// publish writes a release manifest recording digest for the images given. It
// stands in for `intropy release create` without needing it.
func (f releaseFixture) publish(t *testing.T, m *release.Manifest) *release.Manifest {
	t.Helper()
	ref := release.Ref(release.ReleasesRepo(f.image), m.Version)
	if _, err := release.Push(context.Background(), f.reg, ref, m); err != nil {
		t.Fatal(err)
	}
	return m
}

// manifest builds a valid release manifest for the fixture's image.
func (f releaseFixture) manifest(version, digest string) *release.Manifest {
	return &release.Manifest{
		SchemaVersion: release.SchemaVersion,
		Component:     "order-extractor",
		Version:       version,
		CreatedAt:     time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		Source:        release.Source{Commit: testReleaseCommit, Ref: "main"},
		Images:        []release.Image{{Name: f.image, Digest: digest}},
		ChangeBasis:   release.ChangeBasis{Kind: release.BasisInitial},
	}
}

// releaseOptions deploys a version to an environment from a directory that is
// not a git repository at all, which is the point of deploying a release.
func (f releaseFixture) releaseOptions(t *testing.T, version, env string, stdout, stderr *bytes.Buffer) Options {
	t.Helper()
	opts := f.options(stdout, stderr)
	opts.Version = version
	opts.Environment = env
	opts.SourceDir = filepath.Join(t.TempDir(), "not-a-repo")
	return opts
}

// failIfResolverUsed makes the image-tag resolver fail the test, proving a
// release deploy consults no image registry for a digest it already has.
func failIfResolverUsed(t *testing.T) {
	t.Helper()
	original := source.NewResolver
	source.NewResolver = func(string) (source.Resolver, error) {
		t.Error("a release deploy must not resolve image tags: the manifest already records the digests")
		return nil, errors.New("resolver must not be used")
	}
	t.Cleanup(func() { source.NewResolver = original })
}

// The load-bearing test for the whole step: a version deploys, from a directory
// that is not a repository, without any registry tag lookup.
func TestRunDeploysAReleaseWithoutTouchingTheSource(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))
	failIfResolverUsed(t)

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.OutputFormat = OutputJSON
	opts.NoWait = true

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("deploying a release should need no source repository: %v\nstderr: %s", err, stderr.String())
	}

	res := decodeResult(t, stdout.Bytes())
	if res.Release != testVersion {
		t.Errorf("Release = %q, want %q", res.Release, testVersion)
	}
	if res.SourceCommit != testReleaseCommit {
		t.Errorf("SourceCommit = %q, want the manifest's commit %q", res.SourceCommit, testReleaseCommit)
	}
	if len(res.Pins) != 1 || res.Pins[0].Digest != testDigest {
		t.Errorf("pins = %+v, want the manifest's digest %s", res.Pins, testDigest)
	}
	if res.Pins[0].Tag != "" {
		t.Errorf("Tag = %q, want empty: a release records digests, not tags", res.Pins[0].Tag)
	}
	if res.Environment != "staging" {
		t.Errorf("Environment = %q, want staging", res.Environment)
	}
}

// The overlay must record the manifest's commit, not the deploying machine's.
func TestRunReleaseAnnotatesTheManifestCommit(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	overlay := filepath.Join(f.cloneOrigin(t), filepath.FromSlash("domains/orders/order-flow/order-extractor/overlays/staging"))
	k, _, err := kustomize.ReadKustomization(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if got := k.CommonAnnotations[kustomize.AnnotationSourceCommit]; got != testReleaseCommit {
		t.Errorf("source-commit annotation = %q, want the manifest's commit %q", got, testReleaseCommit)
	}
	img, found := k.FindImage(f.image)
	if !found || img.Digest != testDigest {
		t.Errorf("staging overlay pins %+v, want digest %s", img, testDigest)
	}
}

// The overlay must record the version too, because that is the only place a
// promotion can learn it from: promotion copies digests, and a digest does not
// say which release it belongs to.
func TestRunReleaseAnnotatesTheVersion(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	if got := f.stagingAnnotations(t)[kustomize.AnnotationRelease]; got != testVersion {
		t.Errorf("release annotation = %q, want %q", got, testVersion)
	}
}

// A commit deploy over a release-deployed overlay must clear the annotation. A
// version left beside an unrelated digest is read as fact by promote, which
// would then promote a version that environment never ran — worse than having
// no annotation at all.
func TestRunCommitDeployClearsAStaleReleaseAnnotation(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	// Now deploy the current commit to the same environment.
	other := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	stubDigest(t, other)

	stdout.Reset()
	stderr.Reset()
	commitOpts := f.options(&stdout, &stderr)
	commitOpts.Environment = "staging"
	commitOpts.NoWait = true
	if err := Run(context.Background(), commitOpts); err != nil {
		t.Fatalf("deploying the current commit over a release: %v\nstderr: %s", err, stderr.String())
	}

	annotations := f.stagingAnnotations(t)
	if got, found := annotations[kustomize.AnnotationRelease]; found {
		t.Errorf("release annotation = %q, want it removed: these digests came from a commit", got)
	}
	if annotations[kustomize.AnnotationSourceCommit] == testReleaseCommit {
		t.Error("source-commit annotation still names the release's commit")
	}
}

// stagingAnnotations reads the staging overlay's commonAnnotations from a fresh
// clone of the GitOps origin.
func (f releaseFixture) stagingAnnotations(t *testing.T) map[string]string {
	t.Helper()
	overlay := filepath.Join(f.cloneOrigin(t), filepath.FromSlash("domains/orders/order-flow/order-extractor/overlays/staging"))
	k, _, err := kustomize.ReadKustomization(overlay)
	if err != nil {
		t.Fatal(err)
	}
	return k.CommonAnnotations
}

func TestRunReleaseCommitCarriesTheVersion(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	subject := gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%s", "main")
	if !strings.Contains(subject, testVersion) {
		t.Errorf("commit subject %q should name the release", subject)
	}
	trailers := gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%(trailers:only=true)", "main")
	if !strings.Contains(trailers, TrailerRelease+": "+testVersion) {
		t.Errorf("trailers should carry %s:\n%s", TrailerRelease, trailers)
	}
	// The provenance trailer must survive: a release deploy still has a commit.
	if !strings.Contains(trailers, TrailerSourceCommit+": "+testReleaseCommit) {
		t.Errorf("trailers should still carry the source commit:\n%s", trailers)
	}
}

func TestRunUnknownReleaseNamesReleaseCreate(t *testing.T) {
	f := newReleaseFixture(t)
	f.publish(t, f.manifest(testVersion, testDigest))

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.releaseOptions(t, "9.9.9", "staging", &stdout, &stderr))
	if err == nil {
		t.Fatal("deploying a version that was never published should fail")
	}
	if !errors.Is(err, release.ErrNotFound) {
		t.Errorf("error should wrap release.ErrNotFound, got %v", err)
	}
	for _, want := range []string{"intropy release create", testVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
}

func TestRunReleaseRejectsManifestWhoseVersionDiffersFromRequestedTag(t *testing.T) {
	f := newReleaseFixture(t)
	m := f.manifest("1.4.1", testDigest)
	// Publish a valid release under a different OCI tag. A retagged or copied
	// artifact must not make `deploy ... 1.4.2` silently deploy release 1.4.1.
	ref := release.Ref(release.ReleasesRepo(f.image), testVersion)
	if _, err := release.Push(context.Background(), f.reg, ref, m); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.releaseOptions(t, testVersion, "staging", &stdout, &stderr))
	if err == nil {
		t.Fatal("a manifest whose version differs from its requested tag must be refused")
	}
	for _, want := range []string{testVersion, "1.4.1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
	f.requireNothingWritten(t)
}

func TestRunReleaseWithNothingPublishedSaysSo(t *testing.T) {
	f := newReleaseFixture(t)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.releaseOptions(t, testVersion, "staging", &stdout, &stderr))
	if err == nil {
		t.Fatal("deploying from an empty releases repository should fail")
	}
	if !strings.Contains(err.Error(), "nothing has been released") {
		t.Errorf("error should distinguish 'never released' from 'no such version':\n%v", err)
	}
}

func TestRunReleaseMissingADeclaredImageIsRefused(t *testing.T) {
	f := newReleaseFixture(t)
	m := f.manifest(testVersion, testDigest)
	// A release cut before this image was declared: right component, wrong
	// image repository.
	m.Images = []release.Image{{Name: "harbor.intropy.io/integrations/something-else", Digest: testDigest}}
	f.publish(t, m)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.releaseOptions(t, testVersion, "staging", &stdout, &stderr))
	if err == nil {
		t.Fatal("a release with no digest for a declared image should be refused")
	}
	for _, want := range []string{"has no digest for", f.image, "release create"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
}

func TestRunReleaseComponentMismatchIsRefused(t *testing.T) {
	f := newReleaseFixture(t)
	m := f.manifest(testVersion, testDigest)
	m.Component = "order-loader"
	f.publish(t, m)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.releaseOptions(t, testVersion, "staging", &stdout, &stderr))
	if err == nil {
		t.Fatal("a manifest describing another component should be refused")
	}
	for _, want := range []string{"order-loader", "order-extractor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name both components, missing %q:\n%v", want, err)
		}
	}
}

// Dropping an image from component.yaml must not make earlier releases
// undeployable — the recorded digest is simply unused.
func TestRunReleaseExtraImageWarnsButProceeds(t *testing.T) {
	f := newReleaseFixture(t)
	m := f.manifest(testVersion, testDigest)
	m.Images = append(m.Images, release.Image{Name: "harbor.intropy.io/integrations/retired-sidecar", Digest: testDigest})
	f.publish(t, m)

	var stdout, stderr bytes.Buffer
	opts := f.releaseOptions(t, testVersion, "staging", &stdout, &stderr)
	opts.OutputFormat = OutputJSON
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("an undeclared image in the release should not fail the deploy: %v", err)
	}

	if !strings.Contains(stderr.String(), "no longer declares") {
		t.Errorf("stderr should warn about the undeclared image:\n%s", stderr.String())
	}
	if res := decodeResult(t, stdout.Bytes()); len(res.Pins) != 1 {
		t.Errorf("only the declared image should be pinned, got %+v", res.Pins)
	}
}

// cloneOrigin clones the GitOps origin so a test can read what was pushed
// without racing the deploy cache's lock.
func (f runFixture) cloneOrigin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gitops-readback")
	if err := git.Clone(context.Background(), command.ExecRunner{}, f.gitopsOrigin, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}
