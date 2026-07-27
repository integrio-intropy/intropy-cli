package deploy

import (
	"context"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
	"os/exec"
	"strings"
	"testing"
)

func requireKustomize(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize is not installed")
	}
}

// planFixture opens a GitOps repository holding one component whose base
// references image, with the dev overlay pinned as given.
func planFixture(t *testing.T, image, overlayImages string) (*gitops.Repository, gitops.Coordinate, *gitops.ComponentConfig, string) {
	t.Helper()
	requireKustomize(t)

	coord := gitops.Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	origin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:    coord.String(),
		Image:         image,
		Environments:  []string{"dev", "prod"},
		OverlayImages: overlayImages,
	})

	ctx := context.Background()
	repo, err := gitops.Open(ctx, gitops.Options{URL: origin, Runner: command.ExecRunner{}, CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })

	comp, err := gitops.LoadComponentConfig(gitops.JoinRel(repo.Root, coord.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	overlayDir, err := gitops.ResolveOverlay(repo.Root, coord, comp, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return repo, coord, comp, overlayDir
}

const testDigest = "sha256:abc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abcd"

func planOpts(repo *gitops.Repository, coord gitops.Coordinate, overlayDir, image, digest string) PlanOptions {
	return PlanOptions{
		Repository:  repo,
		Kustomize:   kustomize.Client{Runner: command.ExecRunner{}},
		Coordinate:  coord,
		Environment: "dev",
		Source:      source.State{Commit: testCommit, Branch: "main"},
		Pins:        []source.Pin{{Image: image, Digest: digest, Tag: source.CommitTag(testCommit)}},
		OverlayDir:  overlayDir,
		Palette:     kustomize.PlainPalette,
	}
}

func TestBuildPlanPinsTagToDigest(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	repo, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    newTag: latest\n")

	plan, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Empty() {
		t.Fatal("expected a non-empty plan")
	}
	if !plan.DigestChanged {
		t.Error("DigestChanged should be true")
	}
	if plan.ProvenanceOnly() {
		t.Error("ProvenanceOnly should be false when the digest changes")
	}
	if got := plan.Previous[image]; got != ":latest" {
		t.Errorf("Previous[%s] = %q, want %q", image, got, ":latest")
	}
	if !strings.Contains(plan.Diff, testDigest) {
		t.Errorf("diff should show the new digest:\n%s", plan.Diff)
	}
	if !strings.Contains(plan.Diff, kustomize.AnnotationSourceCommit) {
		t.Errorf("diff should show the source-commit annotation:\n%s", plan.Diff)
	}
	if !strings.Contains(plan.Summary(), ":latest") || !strings.Contains(plan.Summary(), testDigest) {
		t.Errorf("summary should show the transition:\n%s", plan.Summary())
	}
}

// Re-running an unchanged deploy must be a clean no-op, and must leave the
// shared checkout untouched so the next run starts from a known state.
func TestBuildPlanAlreadyPinnedIsEmptyAndReverts(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	repo, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    digest: "+testDigest+"\ncommonAnnotations:\n  "+kustomize.AnnotationSourceCommit+": "+testCommit+"\n")

	plan, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() {
		t.Errorf("expected an empty plan, got diff:\n%s", plan.Diff)
	}

	dirty, err := repo.Git.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("an empty plan must leave the checkout clean, got %v", dirty)
	}
}

// The digest is unchanged but the commit moved, so only the annotation differs.
// kustomize propagates commonAnnotations into pod templates, so this really
// does restart the pods — the plan has to flag it rather than hide it.
func TestBuildPlanProvenanceOnlyChange(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	previousCommit := "0123456789abcdef0123456789abcdef01234567"
	repo, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    digest: "+testDigest+"\ncommonAnnotations:\n  "+kustomize.AnnotationSourceCommit+": "+previousCommit+"\n")

	plan, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Empty() {
		t.Fatal("a changed source commit should still produce a diff")
	}
	if plan.DigestChanged {
		t.Error("DigestChanged should be false when only the commit moved")
	}
	if !plan.ProvenanceOnly() {
		t.Error("ProvenanceOnly should be true")
	}
	// Proof that the pods roll: the annotation reaches the pod template.
	if !strings.Contains(plan.Diff, "template") && !strings.Contains(plan.Diff, testCommit) {
		t.Errorf("diff should show the annotation change:\n%s", plan.Diff)
	}
}

// The failure mode that makes an empty diff untrustworthy: kustomize happily
// adds an images[] entry that matches nothing in the base, so the pin is inert,
// the render is unchanged, and "already at that digest" would be a lie.
func TestBuildPlanRejectsInertPin(t *testing.T) {
	// The base references a different repository than component.yaml declares.
	repo, coord, _, overlayDir := planFixture(t, "harbor.intropy.io/integrations/something-else", "")

	unmatched := "harbor.intropy.io/integrations/order-extractor"
	_, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, unmatched, testDigest))
	if err == nil {
		t.Fatal("expected an error: the pin cannot affect the rendered output")
	}
	if !strings.Contains(err.Error(), "had no effect") {
		t.Errorf("error %q should say the pin had no effect", err)
	}
	if !strings.Contains(err.Error(), "Does the base reference") {
		t.Errorf("error %q should point at the likely cause", err)
	}

	// And the failed attempt must not leave the checkout dirty.
	dirty, err := repo.Git.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("a failed plan must revert its edits, got %v", dirty)
	}
}

// The loophole a bare-digest check would leave open, and the reason the guard
// matches the full name@digest: the overlay already pins this very digest to a
// *different* image, so searching the render for the digest alone would find it
// and wrongly declare the inert pin applied. Sibling images built from one
// source commonly share a digest, so this is not a contrived case.
func TestBuildPlanRejectsInertPinWhenDigestExistsElsewhere(t *testing.T) {
	referenced := "harbor.intropy.io/integrations/order-extractor"
	declared := "harbor.intropy.io/integrations/unreferenced"

	// The base uses `referenced`, already pinned to testDigest.
	repo, coord, _, overlayDir := planFixture(t, referenced,
		"images:\n  - name: "+referenced+"\n    digest: "+testDigest+"\n")

	// component.yaml declares an image the base never mentions, resolving to
	// the same digest that is already present in the render.
	_, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, declared, testDigest))
	if err == nil {
		t.Fatal("expected an error: the pin cannot affect the rendered output even though the digest appears in it")
	}
	if !strings.Contains(err.Error(), "had no effect") {
		t.Errorf("error %q should say the pin had no effect", err)
	}
	if !strings.Contains(err.Error(), declared) {
		t.Errorf("error %q should name the image that failed to apply", err)
	}
}

// `kustomize edit set image <name>@<digest>` replaces the entire images[]
// entry, so a previous newName rewrite is dropped and the render carries the
// declared repository. This documents that behaviour, which is what lets the
// guard above look for the declared name alone.
func TestBuildPlanSetImageReplacesNewNameRewrite(t *testing.T) {
	declared := "harbor.intropy.io/integrations/order-extractor"
	rewritten := "mirror.example.com/integrations/order-extractor"

	repo, coord, _, overlayDir := planFixture(t, declared,
		"images:\n  - name: "+declared+"\n    newName: "+rewritten+"\n    newTag: latest\n")

	plan, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, declared, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Diff, declared+"@"+testDigest) {
		t.Errorf("the pin should render under the declared name:\n%s", plan.Diff)
	}

	edited, _, err := kustomize.ReadKustomization(overlayDir)
	if err != nil {
		t.Fatal(err)
	}
	img, ok := edited.FindImage(declared)
	if !ok {
		t.Fatalf("images entry missing after the edit: %+v", edited.Images)
	}
	if img.NewName != "" || img.NewTag != "" {
		t.Errorf("setting a digest should clear newName and newTag, got %+v", img)
	}
}

// An overlay with no images[] entry at all is the common state of existing
// repositories; the pin has to work there as long as the base matches.
func TestBuildPlanAddsMissingImagesEntry(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	repo, coord, _, overlayDir := planFixture(t, image, "")

	plan, err := BuildPlan(context.Background(), planOpts(repo, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Empty() {
		t.Fatal("expected a non-empty plan")
	}
	if got := plan.Previous[image]; got != "" {
		t.Errorf("Previous[%s] = %q, want empty for an absent entry", image, got)
	}
	if !strings.Contains(plan.Summary(), "(absent)") {
		t.Errorf("summary should mark the image as previously absent:\n%s", plan.Summary())
	}
}

func TestPlanRevertRestoresOverlay(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	repo, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    newTag: latest\n")
	ctx := context.Background()

	plan, err := BuildPlan(ctx, planOpts(repo, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := repo.Git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) == 0 {
		t.Fatal("a non-empty plan should leave the overlay edited")
	}

	if err := plan.Revert(ctx, repo); err != nil {
		t.Fatal(err)
	}
	dirty, err = repo.Git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("Revert should restore the overlay, got %v", dirty)
	}
}

// coordFixture is the coordinate every fixture in this package uses.
var coordFixture = gitops.Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}

// repoFixture is a GitOps checkout with a pushable origin, for the publish
// tests: they need to observe what actually landed on the remote.
type repoFixture struct {
	repo       *gitops.Repository
	origin     string
	image      string
	overlayDir string
	comp       *gitops.ComponentConfig
}

func newRepoFixture(t *testing.T) *repoFixture {
	t.Helper()
	requireKustomize(t)

	image := "harbor.intropy.io/integrations/order-extractor"
	origin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:    coordFixture.String(),
		Image:         image,
		Environments:  []string{"dev", "prod"},
		OverlayImages: "images:\n  - name: " + image + "\n    newTag: latest\n",
	})

	ctx := context.Background()
	repo, err := gitops.Open(ctx, gitops.Options{URL: origin, Runner: command.ExecRunner{}, CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	// The checkout commits, so it needs an identity of its own.
	gittest.Run(t, repo.Root, "config", "user.email", "deploy@example.com")
	gittest.Run(t, repo.Root, "config", "user.name", "Deploy")
	gittest.Run(t, repo.Root, "config", "commit.gpgsign", "false")

	comp, err := gitops.LoadComponentConfig(gitops.JoinRel(repo.Root, coordFixture.RelPath()))
	if err != nil {
		t.Fatal(err)
	}
	overlayDir, err := gitops.ResolveOverlay(repo.Root, coordFixture, comp, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return &repoFixture{repo: repo, origin: origin, image: image, overlayDir: overlayDir, comp: comp}
}

// buildPlan produces a real, applied plan: the overlay is edited on disk and
// ready to commit.
func (f *repoFixture) buildPlan(t *testing.T, digest string) *Plan {
	t.Helper()
	plan, err := BuildPlan(context.Background(), planOpts(f.repo, coordFixture, f.overlayDir, f.image, digest))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Empty() {
		t.Fatal("fixture plan is empty; there is nothing to publish")
	}
	return plan
}
