package release

import (
	"context"
	"errors"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
	"github.com/integrio-intropy/intropy-cli/internal/registry/registrytest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/auth"
)

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

func TestReleasesRepoSitsBesideTheImage(t *testing.T) {
	got := ReleasesRepo("harbor.intropy.io/integrations/order-extractor")
	want := "harbor.intropy.io/integrations/order-extractor/releases"
	if got != want {
		t.Errorf("ReleasesRepo() = %q, want %q", got, want)
	}
}

func TestPushPullRoundTrip(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	ref := Ref(ReleasesRepo(srv.Host+"/order-extractor"), "1.4.2")

	want := validManifest()
	digest, err := Push(ctx, c, ref, want)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Error("Push() returned no digest")
	}

	got, err := Pull(ctx, c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SameRelease(want) {
		t.Errorf("round trip changed the release:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("createdAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestPullMissingReleaseIsNotFound(t *testing.T) {
	c, srv := testRegistry(t)

	_, err := Pull(context.Background(), c, Ref(ReleasesRepo(srv.Host+"/order-extractor"), "9.9.9"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v should be ErrNotFound", err)
	}
}

// Something else occupying the tag must never read as absent: publishing over
// it would destroy whatever it is.
func TestPullRejectsANonRelease(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	ref := srv.Host + "/order-extractor/releases:1.4.2"

	if _, err := c.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: "application/vnd.something.else",
		Config:       registry.Blob{MediaType: ocispec.MediaTypeImageConfig, Data: []byte(`{}`)},
		Layers:       []registry.Blob{{MediaType: ocispec.MediaTypeImageLayer, Data: []byte("not a release")}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := Pull(ctx, c, ref)
	if !errors.Is(err, ErrNotRelease) {
		t.Fatalf("error %v should be ErrNotRelease", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("an occupied tag must not read as absent")
	}
}

// A manifest that fails validation is a corrupt release, not a missing one.
func TestPullRejectsAnInvalidManifest(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	ref := srv.Host + "/order-extractor/releases:1.4.2"

	if _, err := c.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: ArtifactType,
		Layers:       []registry.Blob{{MediaType: MediaTypeJSON, Data: []byte(`{"schemaVersion":1,"component":"x"}`)}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := Pull(ctx, c, ref)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error %v should wrap ErrInvalidManifest", err)
	}
}

func TestListVersions(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	repo := ReleasesRepo(srv.Host + "/order-extractor")

	for _, v := range []string{"1.4.0", "1.4.1"} {
		m := validManifest()
		m.Version = v
		if _, err := Push(ctx, c, Ref(repo, v), m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ListVersions(ctx, c, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("ListVersions() = %v, want 2 versions", got)
	}
}

// "Never released" must be distinguishable from "released and then emptied",
// because the first is the routine first-release path.
func TestListVersionsNeverReleasedIsNotFound(t *testing.T) {
	c, srv := testRegistry(t)

	_, err := ListVersions(context.Background(), c, ReleasesRepo(srv.Host+"/brand-new"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v should be ErrNotFound", err)
	}
}
