package deploy

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireKustomize(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize is not installed")
	}
}

const baseDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
spec:
  replicas: 1
  selector:
    matchLabels:
      app: order-extractor
  template:
    metadata:
      labels:
        app: order-extractor
    spec:
      containers:
        - name: app
          image: IMAGE:latest
`

// planFixture builds a GitOps repository containing one component whose base
// references image, with the overlay pinned as given.
func planFixture(t *testing.T, image, overlayImages string) (wt *Worktree, coord Coordinate, comp *ComponentConfig, overlayDir string) {
	t.Helper()
	requireKustomize(t)

	origin := t.TempDir()
	runGit(t, origin, "init", "--quiet", "--initial-branch=main")
	runGit(t, origin, "config", "user.email", "test@example.com")
	runGit(t, origin, "config", "user.name", "Test")
	runGit(t, origin, "config", "commit.gpgsign", "false")

	coord = Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	compRel := filepath.FromSlash(coord.RelPath())

	writeFile(t, filepath.Join(origin, DeployFileName), validDeployYAML)
	writeFile(t, filepath.Join(origin, compRel, ComponentFileName),
		"schemaVersion: 1\nname: order-extractor\nsourcePaths: [src/]\nimages:\n  - name: "+image+"\nenvironments: [dev, prod]\n")
	writeFile(t, filepath.Join(origin, compRel, "base", "deployment.yaml"),
		strings.ReplaceAll(baseDeployment, "IMAGE", image))
	writeFile(t, filepath.Join(origin, compRel, "base", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n")
	writeFile(t, filepath.Join(origin, compRel, OverlaysDirName, "dev", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\n"+overlayImages)

	runGit(t, origin, "add", ".")
	runGit(t, origin, "commit", "--quiet", "-m", "onboard order-extractor")

	ctx := context.Background()
	wt, err := OpenWorktree(ctx, WorktreeOptions{URL: origin, Runner: ExecRunner{}, CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wt.Close() })

	comp, err = LoadComponentConfig(filepath.Join(wt.Root, compRel))
	if err != nil {
		t.Fatal(err)
	}
	overlayDir, err = ResolveOverlay(wt.Root, coord, comp, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return wt, coord, comp, overlayDir
}

const testDigest = "sha256:abc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abcd"

func planOpts(wt *Worktree, coord Coordinate, overlayDir, image, digest string) PlanOptions {
	return PlanOptions{
		Worktree:    wt,
		Kustomize:   Kustomize{Runner: ExecRunner{}},
		Coordinate:  coord,
		Environment: "dev",
		Source:      SourceState{Commit: testCommit, Branch: "main"},
		Pins:        []Pin{{Image: image, Digest: digest, Tag: CommitTag(testCommit)}},
		OverlayDir:  overlayDir,
		Palette:     PlainPalette,
	}
}

func TestBuildPlanPinsTagToDigest(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	wt, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    newTag: latest\n")

	plan, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, image, testDigest))
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
	if !strings.Contains(plan.Diff, AnnotationSourceCommit) {
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
	wt, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    digest: "+testDigest+"\ncommonAnnotations:\n  "+AnnotationSourceCommit+": "+testCommit+"\n")

	plan, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() {
		t.Errorf("expected an empty plan, got diff:\n%s", plan.Diff)
	}

	dirty, err := wt.Git.Status(context.Background())
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
	wt, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    digest: "+testDigest+"\ncommonAnnotations:\n  "+AnnotationSourceCommit+": "+previousCommit+"\n")

	plan, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, image, testDigest))
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
	wt, coord, _, overlayDir := planFixture(t, "harbor.intropy.io/integrations/something-else", "")

	unmatched := "harbor.intropy.io/integrations/order-extractor"
	_, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, unmatched, testDigest))
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
	dirty, err := wt.Git.Status(context.Background())
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
	wt, coord, _, overlayDir := planFixture(t, referenced,
		"images:\n  - name: "+referenced+"\n    digest: "+testDigest+"\n")

	// component.yaml declares an image the base never mentions, resolving to
	// the same digest that is already present in the render.
	_, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, declared, testDigest))
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

	wt, coord, _, overlayDir := planFixture(t, declared,
		"images:\n  - name: "+declared+"\n    newName: "+rewritten+"\n    newTag: latest\n")

	plan, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, declared, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Diff, declared+"@"+testDigest) {
		t.Errorf("the pin should render under the declared name:\n%s", plan.Diff)
	}

	edited, _, err := ReadKustomization(overlayDir)
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
	wt, coord, _, overlayDir := planFixture(t, image, "")

	plan, err := BuildPlan(context.Background(), planOpts(wt, coord, overlayDir, image, testDigest))
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
	wt, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    newTag: latest\n")
	ctx := context.Background()

	plan, err := BuildPlan(ctx, planOpts(wt, coord, overlayDir, image, testDigest))
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := wt.Git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) == 0 {
		t.Fatal("a non-empty plan should leave the overlay edited")
	}

	if err := plan.Revert(ctx, wt); err != nil {
		t.Fatal(err)
	}
	dirty, err = wt.Git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("Revert should restore the overlay, got %v", dirty)
	}
}

func TestReadKustomization(t *testing.T) {
	image := "harbor.intropy.io/integrations/order-extractor"
	wt, coord, _, overlayDir := planFixture(t, image,
		"images:\n  - name: "+image+"\n    newTag: v1.2.3\ncommonAnnotations:\n  "+AnnotationSourceCommit+": deadbeef\n")
	_ = wt
	_ = coord

	k, path, err := ReadKustomization(overlayDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "kustomization.yaml" {
		t.Errorf("path = %q", path)
	}
	img, ok := k.FindImage(image)
	if !ok {
		t.Fatalf("FindImage(%s) not found in %+v", image, k.Images)
	}
	if img.NewTag != "v1.2.3" || img.Pinned() != ":v1.2.3" {
		t.Errorf("image = %+v, Pinned() = %q", img, img.Pinned())
	}
	if k.CommonAnnotations[AnnotationSourceCommit] != "deadbeef" {
		t.Errorf("CommonAnnotations = %v", k.CommonAnnotations)
	}
}

func TestKustomizationPathMissing(t *testing.T) {
	if _, err := KustomizationPath(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory with no kustomization file")
	}
}

func TestKustomizeImagePinned(t *testing.T) {
	cases := []struct {
		img  KustomizeImage
		want string
	}{
		{KustomizeImage{Digest: "sha256:abc"}, "sha256:abc"},
		{KustomizeImage{NewTag: "1.2.3"}, ":1.2.3"},
		{KustomizeImage{}, "(unpinned)"},
		// A digest wins over a tag, matching how kustomize resolves them.
		{KustomizeImage{Digest: "sha256:abc", NewTag: "1.2.3"}, "sha256:abc"},
	}
	for _, tc := range cases {
		if got := tc.img.Pinned(); got != tc.want {
			t.Errorf("Pinned(%+v) = %q, want %q", tc.img, got, tc.want)
		}
	}
}
