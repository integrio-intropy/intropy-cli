package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/registry"
	"github.com/integrio-intropy/intropy-cli/internal/registry/registrytest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const testCommit = "def456abc789def456abc789def456abc789def4"

// testRegistry starts an in-memory registry and returns a client for it.
func testRegistry(t *testing.T) (*registry.Client, *registrytest.Server) {
	t.Helper()
	srv := registrytest.NewServer()
	t.Cleanup(srv.Close)

	c, err := registry.NewClient(
		registry.WithCredentials(func(context.Context, string) (auth.Credential, error) {
			return auth.EmptyCredential, nil
		}),
		registry.WithHTTPClient(srv.Client()),
		registry.WithPlainHTTP(func(string) bool { return true }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func testImageArtifact(title string) registry.Artifact {
	return registry.Artifact{
		ArtifactType: "application/vnd.test.image",
		Config:       registry.Blob{MediaType: ocispec.MediaTypeImageConfig, Data: []byte(`{}`)},
		Layers: []registry.Blob{
			{MediaType: ocispec.MediaTypeImageLayer, Data: []byte(title)},
		},
	}
}

func componentWithImages(names ...string) *gitops.ComponentConfig {
	imgs := make([]gitops.ImageRef, 0, len(names))
	for _, n := range names {
		imgs = append(imgs, gitops.ImageRef{Name: n})
	}
	return &gitops.ComponentConfig{SchemaVersion: 1, Name: "order-extractor", Images: imgs, Environments: []string{"dev"}}
}

func TestCommitTag(t *testing.T) {
	if got, want := CommitTag(testCommit), "sha-"+testCommit; got != want {
		t.Errorf("CommitTag() = %q, want %q", got, want)
	}
}

func TestResolveDigests(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	image := srv.Host + "/integrations/order-extractor"

	pushed, err := c.PushArtifact(ctx, image+":"+CommitTag(testCommit), testImageArtifact("amd64"))
	if err != nil {
		t.Fatal(err)
	}

	pins, err := ResolveDigests(ctx, c, componentWithImages(image), testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 {
		t.Fatalf("pins = %+v, want 1", pins)
	}
	if pins[0].Digest != pushed.Digest {
		t.Errorf("Digest = %q, want %q", pins[0].Digest, pushed.Digest)
	}
	if pins[0].Image != image {
		t.Errorf("Image = %q, want %q", pins[0].Image, image)
	}
	if want := image + "@" + pushed.Digest; pins[0].Ref() != want {
		t.Errorf("Ref() = %q, want %q", pins[0].Ref(), want)
	}
}

// For a multi-architecture build the tag points at an image index, and the
// index digest is what must be pinned — pinning a per-architecture child would
// deploy one architecture to every node.
func TestResolveDigestsPinsTheIndexNotAChild(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	image := srv.Host + "/integrations/order-extractor"

	amd64, err := c.PushArtifact(ctx, image+":arch-amd64", testImageArtifact("amd64"))
	if err != nil {
		t.Fatal(err)
	}
	arm64, err := c.PushArtifact(ctx, image+":arch-arm64", testImageArtifact("arm64"))
	if err != nil {
		t.Fatal(err)
	}

	index, err := c.PushIndex(ctx, image+":"+CommitTag(testCommit), registry.Index{
		Manifests: []registry.IndexManifest{
			{Descriptor: amd64, SourceRef: image + ":arch-amd64"},
			{Descriptor: arm64, SourceRef: image + ":arch-arm64"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	pins, err := ResolveDigests(ctx, c, componentWithImages(image), testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if pins[0].Digest != index.Digest {
		t.Errorf("Digest = %q, want the index digest %q", pins[0].Digest, index.Digest)
	}
	if pins[0].Digest == amd64.Digest || pins[0].Digest == arm64.Digest {
		t.Error("pinned a per-architecture child instead of the index")
	}
}

func TestResolveDigestsAllImages(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	first := srv.Host + "/integrations/order-extractor"
	second := srv.Host + "/integrations/order-sidecar"

	a, err := c.PushArtifact(ctx, first+":"+CommitTag(testCommit), testImageArtifact("first"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.PushArtifact(ctx, second+":"+CommitTag(testCommit), testImageArtifact("second"))
	if err != nil {
		t.Fatal(err)
	}

	pins, err := ResolveDigests(ctx, c, componentWithImages(first, second), testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("pins = %+v, want 2", pins)
	}
	if pins[0].Digest != a.Digest || pins[1].Digest != b.Digest {
		t.Errorf("pins = %+v, want the two pushed digests in declaration order", pins)
	}
	// Distinct images must not collapse to one digest.
	if pins[0].Digest == pins[1].Digest {
		t.Error("two different images resolved to the same digest")
	}
}

// The overwhelmingly common failure: the deploy was run before the pipeline
// finished. It has to say that rather than read as a generic 404.
func TestResolveDigestsMissingTagExplainsThePipeline(t *testing.T) {
	c, srv := testRegistry(t)
	image := srv.Host + "/integrations/order-extractor"

	_, err := ResolveDigests(context.Background(), c, componentWithImages(image), testCommit)
	if err == nil {
		t.Fatal("expected an error for an unpublished tag")
	}
	for _, want := range []string{"pipeline has not published", CommitTag(testCommit)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// A partial resolution must fail rather than pin some images and leave others
// on their previous digest, which would deploy a mismatched set.
func TestResolveDigestsFailsIfAnyImageIsMissing(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	present := srv.Host + "/integrations/order-extractor"
	absent := srv.Host + "/integrations/order-sidecar"

	if _, err := c.PushArtifact(ctx, present+":"+CommitTag(testCommit), testImageArtifact("first")); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveDigests(ctx, c, componentWithImages(present, absent), testCommit); err == nil {
		t.Fatal("expected the whole resolution to fail")
	}
}

type stubResolver struct {
	desc registry.Descriptor
	err  error
}

func (s stubResolver) Resolve(context.Context, string) (registry.Descriptor, error) {
	return s.desc, s.err
}

func TestResolveDigestsUnauthorizedIsSurfaced(t *testing.T) {
	_, err := ResolveDigests(context.Background(), stubResolver{err: registry.ErrUnauthorized},
		componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("error %v should wrap ErrUnauthorized so the docker login hint survives", err)
	}
}

// A registry that answers without a digest must not yield an empty pin, which
// would be written into the overlay as "image@".
func TestResolveDigestsRejectsEmptyDigest(t *testing.T) {
	_, err := ResolveDigests(context.Background(), stubResolver{desc: registry.Descriptor{}},
		componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit)
	if err == nil {
		t.Fatal("expected an error for a missing digest")
	}
	if !strings.Contains(err.Error(), "no digest") {
		t.Errorf("error %q should say the registry returned no digest", err)
	}
}
