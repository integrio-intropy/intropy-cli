package registry

import (
	"context"
	"slices"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/integrio-intropy/intropy-cli/internal/registry/registrytest"
)

const (
	testArtifactType = "application/vnd.intropy.test.v1"
	testConfigType   = "application/vnd.intropy.test.config.v1+json"
	testLayerType    = "application/vnd.intropy.test.content.v1"
)

func testClient(t *testing.T, opts ...Option) (*Client, *registrytest.Server) {
	t.Helper()

	srv := registrytest.NewServer()
	t.Cleanup(srv.Close)

	base := []Option{
		WithCredentials(anonymous),
		WithHTTPClient(srv.Client()),
		WithPlainHTTP(func(string) bool { return true }),
	}
	c, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func testArtifact() Artifact {
	return Artifact{
		ArtifactType: testArtifactType,
		Config:       Blob{MediaType: testConfigType, Data: []byte(`{"component":"component-x"}`)},
		Layers:       []Blob{{MediaType: testLayerType, Data: []byte("release notes")}},
		Annotations:  map[string]string{"org.opencontainers.image.title": "component-x"},
	}
}

func TestPushPullArtifactRoundtrip(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()
	ref := srv.Host + "/releases/component-x:1.4.2"

	pushed, err := c.PushArtifact(ctx, ref, testArtifact())
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}
	if pushed.Digest == "" {
		t.Fatal("PushArtifact returned an empty digest")
	}
	if pushed.ArtifactType != testArtifactType {
		t.Errorf("pushed artifactType = %q; want %q", pushed.ArtifactType, testArtifactType)
	}

	got, desc, err := c.PullArtifact(ctx, ref)
	if err != nil {
		t.Fatalf("PullArtifact: %v", err)
	}
	if desc.Digest != pushed.Digest {
		t.Errorf("pulled digest = %q; want %q", desc.Digest, pushed.Digest)
	}
	if got.ArtifactType != testArtifactType {
		t.Errorf("pulled artifactType = %q; want %q", got.ArtifactType, testArtifactType)
	}
	want := testArtifact()
	if got.Config.MediaType != want.Config.MediaType || string(got.Config.Data) != string(want.Config.Data) {
		t.Errorf("pulled config = %+v; want %+v", got.Config, want.Config)
	}
	if len(got.Layers) != 1 {
		t.Fatalf("pulled %d layers; want 1", len(got.Layers))
	}
	if got.Layers[0].MediaType != testLayerType || string(got.Layers[0].Data) != "release notes" {
		t.Errorf("pulled layer = %+v; want %+v", got.Layers[0], want.Layers[0])
	}
	if got.Annotations["org.opencontainers.image.title"] != "component-x" {
		t.Errorf("pulled annotations = %v; want the title annotation preserved", got.Annotations)
	}
}

func TestPushArtifactRequiresTag(t *testing.T) {
	c, srv := testClient(t)

	if _, err := c.PushArtifact(context.Background(), srv.Host+"/releases/component-x", testArtifact()); err == nil {
		t.Error("expected an error when the ref has no tag")
	}
}

func TestResolveByTagAndDigest(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()
	ref := srv.Host + "/releases/component-x:1.4.2"

	pushed, err := c.PushArtifact(ctx, ref, testArtifact())
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	byTag, err := c.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve by tag: %v", err)
	}
	if byTag.Digest != pushed.Digest {
		t.Errorf("resolve by tag digest = %q; want %q", byTag.Digest, pushed.Digest)
	}
	if byTag.Annotations["org.opencontainers.image.title"] != "component-x" {
		t.Errorf("resolve by tag annotations = %v; want the title annotation", byTag.Annotations)
	}
	// Callers tell one kind of artifact from another without pulling it, so the
	// artifact type has to survive a resolve.
	if byTag.ArtifactType != testArtifact().ArtifactType {
		t.Errorf("resolve by tag artifactType = %q; want %q", byTag.ArtifactType, testArtifact().ArtifactType)
	}

	byDigest, err := c.Resolve(ctx, srv.Host+"/releases/component-x@"+pushed.Digest)
	if err != nil {
		t.Fatalf("Resolve by digest: %v", err)
	}
	if byDigest.Digest != pushed.Digest {
		t.Errorf("resolve by digest = %q; want %q", byDigest.Digest, pushed.Digest)
	}
	if byDigest.Size != byTag.Size || byDigest.MediaType != byTag.MediaType {
		t.Errorf("resolve by digest = %+v; want it to match resolve by tag %+v", byDigest, byTag)
	}
}

func TestPullArtifactByDigest(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()

	pushed, err := c.PushArtifact(ctx, srv.Host+"/releases/component-x:1.4.2", testArtifact())
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	_, desc, err := c.PullArtifact(ctx, srv.Host+"/releases/component-x@"+pushed.Digest)
	if err != nil {
		t.Fatalf("PullArtifact by digest: %v", err)
	}
	if desc.Digest != pushed.Digest {
		t.Errorf("pulled digest = %q; want %q", desc.Digest, pushed.Digest)
	}
}

// TestPushIndexCopiesChildren locks in the workaround for registries that
// reject an index whose referenced manifests are absent from the target
// repository.
func TestPushIndexCopiesChildren(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()

	first, err := c.PushArtifact(ctx, srv.Host+"/skills/alpha:1.0.0", testArtifact())
	if err != nil {
		t.Fatalf("push alpha: %v", err)
	}
	second, err := c.PushArtifact(ctx, srv.Host+"/skills/beta:2.0.0", testArtifact())
	if err != nil {
		t.Fatalf("push beta: %v", err)
	}

	index := Index{
		ArtifactType: "application/vnd.intropy.test.collection.v1",
		Annotations:  map[string]string{"io.intropy.collection.name": "default"},
		Manifests: []IndexManifest{
			{
				Descriptor: Descriptor{
					MediaType:    ocispec.MediaTypeImageManifest,
					ArtifactType: testArtifactType,
					Digest:       first.Digest,
					Size:         first.Size,
					Annotations:  map[string]string{"io.intropy.name": "alpha"},
				},
				SourceRef: srv.Host + "/skills/alpha:1.0.0",
			},
			{
				Descriptor: Descriptor{
					MediaType:    ocispec.MediaTypeImageManifest,
					ArtifactType: testArtifactType,
					Digest:       second.Digest,
					Size:         second.Size,
					Annotations:  map[string]string{"io.intropy.name": "beta"},
				},
				SourceRef: srv.Host + "/skills/beta:2.0.0",
			},
		},
	}

	pushed, err := c.PushIndex(ctx, srv.Host+"/skills/index:latest", index)
	if err != nil {
		t.Fatalf("PushIndex: %v", err)
	}
	if pushed.MediaType != ocispec.MediaTypeImageIndex {
		t.Errorf("index mediaType = %q; want %q", pushed.MediaType, ocispec.MediaTypeImageIndex)
	}

	for _, child := range []string{first.Digest, second.Digest} {
		if !srv.Registry.HasManifest("skills/index", child) {
			t.Errorf("child manifest %s was not copied into the index repository", child)
		}
	}

	got, desc, err := c.PullIndex(ctx, srv.Host+"/skills/index:latest")
	if err != nil {
		t.Fatalf("PullIndex: %v", err)
	}
	if desc.Digest != pushed.Digest {
		t.Errorf("pulled index digest = %q; want %q", desc.Digest, pushed.Digest)
	}
	if got.ArtifactType != index.ArtifactType {
		t.Errorf("pulled index artifactType = %q; want %q", got.ArtifactType, index.ArtifactType)
	}
	if got.Annotations["io.intropy.collection.name"] != "default" {
		t.Errorf("pulled index annotations = %v; want the collection name preserved", got.Annotations)
	}
	if len(got.Manifests) != 2 {
		t.Fatalf("pulled %d index entries; want 2", len(got.Manifests))
	}
	names := []string{
		got.Manifests[0].Descriptor.Annotations["io.intropy.name"],
		got.Manifests[1].Descriptor.Annotations["io.intropy.name"],
	}
	if !slices.Contains(names, "alpha") || !slices.Contains(names, "beta") {
		t.Errorf("pulled index entry names = %v; want alpha and beta", names)
	}
}

func TestPushIndexSkipsCopyWithoutSourceRef(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()

	child, err := c.PushArtifact(ctx, srv.Host+"/releases/component-x:1.4.2", testArtifact())
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	index := Index{
		ArtifactType: "application/vnd.intropy.test.collection.v1",
		Manifests: []IndexManifest{{
			Descriptor: Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    child.Digest,
				Size:      child.Size,
			},
		}},
	}

	if _, err := c.PushIndex(ctx, srv.Host+"/releases/component-x:index", index); err != nil {
		t.Fatalf("PushIndex: %v", err)
	}
}

func TestUserAgentIsSent(t *testing.T) {
	c, srv := testClient(t, WithUserAgent("intropy-cli/test"))

	if _, err := c.PushArtifact(context.Background(), srv.Host+"/releases/component-x:1.4.2", testArtifact()); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	agents := srv.Registry.UserAgents()
	if len(agents) == 0 {
		t.Fatal("registry received no requests")
	}
	for _, ua := range agents {
		if ua != "intropy-cli/test" {
			t.Errorf("User-Agent = %q; want %q", ua, "intropy-cli/test")
		}
	}
}
