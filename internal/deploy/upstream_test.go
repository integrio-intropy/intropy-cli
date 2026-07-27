package deploy

import (
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

const upstreamDigest = "sha256:9f2c1ad4b8e0aaaabbbbccccddddeeeeffff00001111222233334444555566ff"

// upstreamRepo builds a GitOps repository whose dev overlay carries the given
// images[]/commonAnnotations block, and returns the root plus what the
// comparison needs.
func upstreamRepo(t *testing.T, devOverlay string) (root string, coord gitops.Coordinate, comp *gitops.ComponentConfig, pins []source.Pin) {
	t.Helper()

	image := "harbor.intropy.io/integrations/order-extractor"
	coord = gitops.Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	root = gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:    coord.String(),
		Image:         image,
		Environments:  []string{"dev", "staging"},
		OverlayImages: devOverlay,
	})

	comp, err := gitops.LoadComponentConfig(gitops.JoinRel(root, coord.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	return root, coord, comp, []source.Pin{{Image: image, Digest: testDigest}}
}

// pinnedOverlay is the block an earlier deployment to dev would have left.
func pinnedOverlay(image, digest, commit string) string {
	return "images:\n  - name: " + image + "\n    digest: " + digest +
		"\ncommonAnnotations:\n  deploy.internal/source-commit: " + commit + "\n"
}

func staging(promotesFrom ...string) gitops.EnvironmentConfig {
	return gitops.EnvironmentConfig{Sync: gitops.SyncAuto, PromotesFrom: promotesFrom}
}

func TestInspectUpstreamsReportsAMatch(t *testing.T) {
	root, coord, comp, pins := upstreamRepo(t, pinnedOverlay(
		"harbor.intropy.io/integrations/order-extractor", testDigest, testReleaseCommit))

	got := InspectUpstreams(root, coord, comp, "staging", staging("dev"), pins)
	if len(got) != 1 {
		t.Fatalf("want one upstream, got %+v", got)
	}
	if got[0].Status != UpstreamMatch {
		t.Errorf("status = %q, want %q", got[0].Status, UpstreamMatch)
	}
	if got[0].SourceCommit != testReleaseCommit {
		t.Errorf("SourceCommit = %q, want %q", got[0].SourceCommit, testReleaseCommit)
	}
	if desc := got[0].Describe(pins); !strings.Contains(desc, "tested bits") {
		t.Errorf("a match should reassure, got %q", desc)
	}
}

func TestInspectUpstreamsReportsADifference(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	root, coord, comp, pins := upstreamRepo(t, pinnedOverlay(image, upstreamDigest, testReleaseCommit))

	got := InspectUpstreams(root, coord, comp, "staging", staging("dev"), pins)
	if got[0].Status != UpstreamDiffers {
		t.Fatalf("status = %q, want %q", got[0].Status, UpstreamDiffers)
	}
	if got[0].Pinned[image] != upstreamDigest {
		t.Errorf("Pinned[%s] = %q, want dev's digest %q", image, got[0].Pinned[image], upstreamDigest)
	}
	desc := got[0].Describe(pins)
	if !strings.Contains(desc, "different digest") || !strings.Contains(desc, "have not run there") {
		t.Errorf("a mismatch should say so plainly, got %q", desc)
	}
}

// An overlay still on a tag has no digest to compare, which is a different
// statement from "it runs something else".
func TestInspectUpstreamsReportsAnUnpinnedUpstream(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	root, coord, comp, pins := upstreamRepo(t, "images:\n  - name: "+image+"\n    newTag: latest\n")

	got := InspectUpstreams(root, coord, comp, "staging", staging("dev"), pins)
	if got[0].Status != UpstreamUnpinned {
		t.Fatalf("status = %q, want %q", got[0].Status, UpstreamUnpinned)
	}
	if desc := got[0].Describe(pins); !strings.Contains(desc, "nothing to compare") {
		t.Errorf("an unpinned upstream should say there is nothing to compare, got %q", desc)
	}
}

// The guarantee that steps 1 and 2 are untouched: dev promotes from nothing, so
// its output gains no line.
func TestInspectUpstreamsIsNilWithoutPromotesFrom(t *testing.T) {
	root, coord, comp, pins := upstreamRepo(t, "")

	if got := InspectUpstreams(root, coord, comp, "dev", gitops.EnvironmentConfig{Sync: gitops.SyncAuto}, pins); got != nil {
		t.Errorf("an environment with no promotesFrom should report nothing, got %+v", got)
	}
}

// deploy.yaml does not forbid an environment promoting from itself, and
// comparing an overlay with the edit about to be made to it is meaningless.
func TestInspectUpstreamsSkipsTheTargetEnvironment(t *testing.T) {
	root, coord, comp, pins := upstreamRepo(t, "")

	if got := InspectUpstreams(root, coord, comp, "staging", staging("staging"), pins); got != nil {
		t.Errorf("the target must not compare against itself, got %+v", got)
	}
}

// Nothing in the comparison may fail a deploy.
func TestInspectUpstreamsSurvivesAMissingOverlay(t *testing.T) {
	root, coord, comp, pins := upstreamRepo(t, "")

	got := InspectUpstreams(root, coord, comp, "staging", staging("nowhere"), pins)
	if len(got) != 1 || got[0].Status != UpstreamUnknown {
		t.Fatalf("an environment the component is not onboarded to should be %q, got %+v", UpstreamUnknown, got)
	}
	if desc := got[0].Describe(pins); !strings.Contains(desc, "nothing to compare") {
		t.Errorf("an unreadable upstream should say there is nothing to compare, got %q", desc)
	}
}

func TestUpstreamDescribe(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	pins := []source.Pin{{Image: image, Digest: testDigest}}

	tests := []struct {
		name     string
		upstream Upstream
		want     string
	}{
		{
			name:     "match with provenance",
			upstream: Upstream{Environment: "dev", Status: UpstreamMatch, SourceCommit: testReleaseCommit},
			want:     "dev already runs this digest (commit 197a3ae) — you are shipping the tested bits",
		},
		{
			name:     "match without provenance",
			upstream: Upstream{Environment: "dev", Status: UpstreamMatch},
			want:     "dev already runs this digest — you are shipping the tested bits",
		},
		{
			name:     "differs",
			upstream: Upstream{Environment: "dev", Status: UpstreamDiffers, Pinned: map[string]string{image: upstreamDigest}},
			want:     "dev runs a different digest for " + image + " (sha256:9f2c1ad4b8e0) — these bits have not run there",
		},
		{
			name:     "unpinned",
			upstream: Upstream{Environment: "dev", Status: UpstreamUnpinned, Pinned: map[string]string{image: ":latest"}},
			want:     "dev pins no digest for " + image + ", so there is nothing to compare",
		},
		{
			name:     "unknown",
			upstream: Upstream{Environment: "dev", Status: UpstreamUnknown},
			want:     "dev could not be read, so there is nothing to compare",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.upstream.Describe(pins); got != tt.want {
				t.Errorf("Describe()\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// With several images the summary stays singular-or-plural correct.
func TestUpstreamDescribePluralisesSeveralImages(t *testing.T) {
	pins := []source.Pin{
		{Image: "harbor.intropy.io/a", Digest: testDigest},
		{Image: "harbor.intropy.io/b", Digest: testDigest},
	}
	u := Upstream{Environment: "dev", Status: UpstreamMatch}
	if got := u.Describe(pins); !strings.Contains(got, "these digests") {
		t.Errorf("two images should read as plural, got %q", got)
	}
}
