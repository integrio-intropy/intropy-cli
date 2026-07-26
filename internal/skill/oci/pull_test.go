package oci_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	orasauth "oras.land/oras-go/v2/registry/remote/auth"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
	"github.com/integrio-intropy/intropy-cli/internal/registry/registrytest"
	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

func testClients(t *testing.T) (*oci.Client, *registry.Client, *registrytest.Server) {
	t.Helper()

	srv := registrytest.NewServer()
	t.Cleanup(srv.Close)

	opts := []registry.Option{
		registry.WithCredentials(func(context.Context, string) (orasauth.Credential, error) {
			return orasauth.EmptyCredential, nil
		}),
		registry.WithHTTPClient(srv.Client()),
		registry.WithPlainHTTP(func(string) bool { return true }),
	}

	skillClient, err := oci.NewClient(opts...)
	if err != nil {
		t.Fatalf("oci.NewClient: %v", err)
	}
	rawClient, err := registry.NewClient(opts...)
	if err != nil {
		t.Fatalf("registry.NewClient: %v", err)
	}
	return skillClient, rawClient, srv
}

func TestPushPullSkillRoundtrip(t *testing.T) {
	client, _, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/skills/pr-review:1.2.0"

	pushed, err := client.Push(ctx, ref, oci.Artifact{
		Config: oci.Config{
			SchemaVersion: oci.SupportedSchemaVersion,
			Name:          "pr-review",
			Description:   "Reviews pull requests",
			License:       "Apache-2.0",
		},
		Content: io.NopCloser(bytes.NewReader([]byte("skill tarball"))),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pushed.ArtifactType != oci.MediaTypeSkillArtifact {
		t.Errorf("pushed artifactType = %q; want %q", pushed.ArtifactType, oci.MediaTypeSkillArtifact)
	}

	got, err := client.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got.Config.Name != "pr-review" {
		t.Errorf("pulled config name = %q; want %q", got.Config.Name, "pr-review")
	}
	// Push stamps the tag onto the published config as the skill version.
	if got.Config.Version != "1.2.0" {
		t.Errorf("pulled config version = %q; want %q", got.Config.Version, "1.2.0")
	}
	if got.Digest != pushed.Digest {
		t.Errorf("pulled digest = %q; want %q", got.Digest, pushed.Digest)
	}
	if got.Tag != "1.2.0" {
		t.Errorf("pulled tag = %q; want %q", got.Tag, "1.2.0")
	}

	content, err := io.ReadAll(got.Content)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != "skill tarball" {
		t.Errorf("pulled content = %q; want %q", content, "skill tarball")
	}
}

func TestPullRejectsNonSkillArtifact(t *testing.T) {
	client, raw, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/releases/component-x:1.4.2"

	if _, err := raw.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: "application/vnd.intropy.release.v1",
		Config:       registry.Blob{MediaType: "application/json", Data: []byte(`{}`)},
		Layers:       []registry.Blob{{MediaType: "application/json", Data: []byte(`{"component":"component-x"}`)}},
	}); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	if _, err := client.Pull(ctx, ref); !errors.Is(err, oci.ErrNotSkill) {
		t.Fatalf("Pull error = %v; want ErrNotSkill", err)
	}
}

func TestPullRejectsMultipleLayers(t *testing.T) {
	client, raw, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/skills/pr-review:1.2.0"

	if _, err := raw.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: oci.MediaTypeSkillArtifact,
		Config: registry.Blob{
			MediaType: oci.MediaTypeSkillConfig,
			Data:      []byte(`{"schemaVersion":"1","name":"pr-review"}`),
		},
		Layers: []registry.Blob{
			{MediaType: oci.MediaTypeSkillContent, Data: []byte("first")},
			{MediaType: oci.MediaTypeSkillContent, Data: []byte("second")},
		},
	}); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	if _, err := client.Pull(ctx, ref); !errors.Is(err, oci.ErrNotSkill) {
		t.Fatalf("Pull error = %v; want ErrNotSkill", err)
	}
}

func TestPullRejectsUnexpectedLayerMediaType(t *testing.T) {
	client, raw, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/skills/pr-review:1.2.0"

	if _, err := raw.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: oci.MediaTypeSkillArtifact,
		Config: registry.Blob{
			MediaType: oci.MediaTypeSkillConfig,
			Data:      []byte(`{"schemaVersion":"1","name":"pr-review"}`),
		},
		Layers: []registry.Blob{{MediaType: "application/gzip", Data: []byte("tarball")}},
	}); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	if _, err := client.Pull(ctx, ref); !errors.Is(err, oci.ErrNotSkill) {
		t.Fatalf("Pull error = %v; want ErrNotSkill", err)
	}
}

func TestPullRejectsInvalidConfig(t *testing.T) {
	client, raw, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/skills/pr-review:1.2.0"

	if _, err := raw.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: oci.MediaTypeSkillArtifact,
		Config: registry.Blob{
			MediaType: oci.MediaTypeSkillConfig,
			Data:      []byte(`{"schemaVersion":"99","name":"pr-review"}`),
		},
		Layers: []registry.Blob{{MediaType: oci.MediaTypeSkillContent, Data: []byte("tarball")}},
	}); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}

	if _, err := client.Pull(ctx, ref); !errors.Is(err, oci.ErrInvalidConfig) {
		t.Fatalf("Pull error = %v; want ErrInvalidConfig", err)
	}
}

func TestPushRequiresTag(t *testing.T) {
	client, _, srv := testClients(t)

	_, err := client.Push(context.Background(), srv.Host+"/skills/pr-review", oci.Artifact{
		Config:  oci.Config{SchemaVersion: oci.SupportedSchemaVersion, Name: "pr-review"},
		Content: io.NopCloser(bytes.NewReader(nil)),
	})
	if err == nil {
		t.Fatal("expected an error when the ref has no tag")
	}
}

func TestPushPullCollectionIndex(t *testing.T) {
	client, _, srv := testClients(t)
	ctx := context.Background()

	skillRef := srv.Host + "/skills/pr-review:1.2.0"
	pushed, err := client.Push(ctx, skillRef, oci.Artifact{
		Config: oci.Config{
			SchemaVersion: oci.SupportedSchemaVersion,
			Name:          "pr-review",
			Description:   "Reviews pull requests",
		},
		Content: io.NopCloser(bytes.NewReader([]byte("skill tarball"))),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	indexRef := srv.Host + "/skills/index:latest"
	if _, err := client.PushIndex(ctx, indexRef, oci.Index{
		Annotations: map[string]string{oci.AnnotationCollectionName: "default"},
		Manifests: []oci.IndexEntry{{
			Name:        "pr-review",
			Ref:         skillRef,
			Version:     "1.2.0",
			Description: "Reviews pull requests",
			Digest:      pushed.Digest,
			Size:        pushed.Size,
		}},
	}); err != nil {
		t.Fatalf("PushIndex: %v", err)
	}

	// The skill manifest must have been copied into the collection
	// repository, or spec-compliant registries reject the index.
	if !srv.Registry.HasManifest("skills/index", pushed.Digest) {
		t.Error("skill manifest was not copied into the collection repository")
	}

	index, err := client.PullIndex(ctx, indexRef)
	if err != nil {
		t.Fatalf("PullIndex: %v", err)
	}
	if index.Annotations[oci.AnnotationCollectionName] != "default" {
		t.Errorf("index annotations = %v; want the collection name preserved", index.Annotations)
	}
	if len(index.Manifests) != 1 {
		t.Fatalf("index has %d entries; want 1", len(index.Manifests))
	}
	entry := index.Manifests[0]
	if entry.Name != "pr-review" || entry.Ref != skillRef || entry.Version != "1.2.0" {
		t.Errorf("index entry = %+v; want name/ref/version round-tripped", entry)
	}
	if entry.Digest != pushed.Digest {
		t.Errorf("index entry digest = %q; want %q", entry.Digest, pushed.Digest)
	}
}

func TestPullIndexRejectsNonCollection(t *testing.T) {
	client, _, srv := testClients(t)
	ctx := context.Background()
	ref := srv.Host + "/skills/pr-review:1.2.0"

	if _, err := client.Push(ctx, ref, oci.Artifact{
		Config:  oci.Config{SchemaVersion: oci.SupportedSchemaVersion, Name: "pr-review"},
		Content: io.NopCloser(bytes.NewReader([]byte("skill tarball"))),
	}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if _, err := client.PullIndex(ctx, ref); err == nil {
		t.Fatal("expected PullIndex to reject a skill artifact")
	}
}

func TestPullNotFound(t *testing.T) {
	client, _, srv := testClients(t)

	_, err := client.Pull(context.Background(), srv.Host+"/skills/missing:1.0.0")
	if !errors.Is(err, oci.ErrNotFound) {
		t.Fatalf("Pull error = %v; want ErrNotFound", err)
	}
}
