package registry

import (
	"context"
	"errors"
	"slices"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// tagsFixture pushes one small artifact under each of tags into repo.
func tagsFixture(t *testing.T, c *Client, repo string, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		art := Artifact{
			ArtifactType: "application/vnd.test.release",
			Config:       Blob{MediaType: ocispec.MediaTypeImageConfig, Data: []byte(`{}`)},
			Layers:       []Blob{{MediaType: ocispec.MediaTypeImageLayer, Data: []byte(tag)}},
		}
		if _, err := c.PushArtifact(context.Background(), repo+":"+tag, art); err != nil {
			t.Fatalf("push %s: %v", tag, err)
		}
	}
}

func TestListTags(t *testing.T) {
	c, srv := testClient(t)
	repo := srv.Host + "/order-extractor/releases"
	tagsFixture(t, c, repo, "1.4.0", "1.4.1", "1.5.0")

	got, err := c.ListTags(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(got)
	want := []string{"1.4.0", "1.4.1", "1.5.0"}
	if !slices.Equal(got, want) {
		t.Errorf("ListTags() = %v, want %v", got, want)
	}
}

// A repository nobody has pushed to must be distinguishable from one whose
// tags were all deleted. The first is a component's first release; the second
// is something to worry about.
func TestListTagsUnknownRepositoryIsNotFound(t *testing.T) {
	c, srv := testClient(t)

	_, err := c.ListTags(context.Background(), srv.Host+"/never-pushed/releases")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v should be ErrNotFound", err)
	}
}

// The tag on the reference names one artifact; the listing is about the whole
// repository, so the tag must be ignored rather than filtered on.
func TestListTagsIgnoresTheReferenceTag(t *testing.T) {
	c, srv := testClient(t)
	repo := srv.Host + "/order-extractor/releases"
	tagsFixture(t, c, repo, "1.4.0", "1.4.1")

	got, err := c.ListTags(context.Background(), repo+":1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("ListTags(%s:1.4.0) = %v, want both tags", repo, got)
	}
}
