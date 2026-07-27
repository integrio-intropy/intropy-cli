package deploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
)

// Plan is the outcome of editing an overlay and comparing the render before and
// after. It is produced without writing to git, so it is safe to build and
// discard.
type Plan struct {
	Coordinate  gitops.Coordinate
	Environment string
	Source      SourceState

	// Pins are the digests resolved for this commit.
	Pins []Pin

	// Previous records how each image was pinned before the edit, keyed by
	// image name, for a readable summary.
	Previous map[string]string

	// Diff is the unified diff of the rendered output. Empty means the edit
	// changed nothing.
	Diff string

	// DigestChanged reports whether any image's digest actually moved.
	DigestChanged bool

	// OverlayDir is the absolute path of the edited overlay.
	OverlayDir string

	// KustomizationPath is the file that was modified.
	KustomizationPath string
}

// Empty reports whether the plan would change nothing.
func (p *Plan) Empty() bool { return p.Diff == "" }

// ProvenanceOnly reports that the render changed but no image digest did — so
// the only difference is the source-commit annotation.
//
// Worth calling out rather than hiding: because kustomize propagates
// commonAnnotations into pod templates, this still rolls the pods. Suppressing
// the annotation instead would leave it disagreeing with the commit actually
// deployed, which is worse than a restart.
func (p *Plan) ProvenanceOnly() bool { return !p.Empty() && !p.DigestChanged }

// Summary renders the one-line-per-image human summary.
func (p *Plan) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s → %s (commit %s)\n", p.Coordinate, p.Environment, p.Source.ShortCommit())
	for _, pin := range p.Pins {
		was := p.Previous[pin.Image]
		if was == "" {
			was = "(absent)"
		}
		fmt.Fprintf(&b, "  %s\n    %s → %s\n", pin.Image, was, pin.Digest)
	}
	return b.String()
}

// PlanOptions configures BuildPlan.
type PlanOptions struct {
	Repository  *gitops.Repository
	Kustomize   kustomize.Client
	Coordinate  gitops.Coordinate
	Environment string
	Source      SourceState
	Pins        []Pin
	OverlayDir  string
	Palette     kustomize.Palette
}

// BuildPlan edits the overlay, renders before and after, and diffs them.
//
// On any failure — and when the diff turns out empty — the overlay is reverted,
// so the caller never has to reason about a half-edited worktree. The revert is
// scoped to the overlay directory: the cached checkout is shared, and an
// unscoped revert would discard anything else in it.
func BuildPlan(ctx context.Context, opts PlanOptions) (*Plan, error) {
	overlayRel := opts.Coordinate.OverlayRelPath(opts.Environment)

	current, kustPath, err := kustomize.ReadKustomization(opts.OverlayDir)
	if err != nil {
		return nil, err
	}

	previous := make(map[string]string, len(opts.Pins))
	digestChanged := false
	for _, pin := range opts.Pins {
		img, found := current.FindImage(pin.Image)
		if !found {
			previous[pin.Image] = ""
			digestChanged = true
			continue
		}
		previous[pin.Image] = img.Pinned()
		if img.Digest != pin.Digest {
			digestChanged = true
		}
	}

	before, err := opts.Kustomize.Build(ctx, opts.OverlayDir)
	if err != nil {
		return nil, err
	}
	beforeNorm, err := kustomize.Normalize(before)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Coordinate:        opts.Coordinate,
		Environment:       opts.Environment,
		Source:            opts.Source,
		Pins:              opts.Pins,
		Previous:          previous,
		DigestChanged:     digestChanged,
		OverlayDir:        opts.OverlayDir,
		KustomizationPath: kustPath,
	}

	revert := func() error {
		return opts.Repository.Git.CheckoutPaths(ctx, overlayRel)
	}

	if err := applyEdits(ctx, opts); err != nil {
		return nil, withRevert(err, revert)
	}

	after, err := opts.Kustomize.Build(ctx, opts.OverlayDir)
	if err != nil {
		return nil, withRevert(err, revert)
	}
	afterNorm, err := kustomize.Normalize(after)
	if err != nil {
		return nil, withRevert(err, revert)
	}

	// An empty diff is not proof that the digest is already deployed. Existing
	// overlays often have no images[] entry, and `kustomize edit set image`
	// silently adds one — if the base never references that repository the pin
	// is inert, the render is unchanged, and reporting "already at that digest"
	// would be a lie. Confirm each pin actually reached the output.
	if err := assertPinsRendered(afterNorm, opts.Pins); err != nil {
		return nil, withRevert(err, revert)
	}

	plan.Diff = kustomize.Diff(beforeNorm, afterNorm, overlayRel+" (current)", overlayRel+" (planned)", opts.Palette)

	// Nothing to commit, so leave the checkout as we found it.
	if plan.Empty() {
		if err := revert(); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func applyEdits(ctx context.Context, opts PlanOptions) error {
	for _, pin := range opts.Pins {
		if err := opts.Kustomize.SetImage(ctx, opts.OverlayDir, pin.Image, pin.Digest); err != nil {
			return err
		}
	}
	return opts.Kustomize.SetAnnotation(ctx, opts.OverlayDir, kustomize.AnnotationSourceCommit, opts.Source.Commit)
}

// assertPinsRendered checks that every pin reached the rendered output as the
// exact reference it should produce.
//
// Matching the full image@digest rather than the digest alone is essential. A
// bare digest search passes whenever the same digest is already pinned to some
// *other* image in the overlay — which happens routinely, since sibling images
// are often built from one source and share a digest — and that would let an
// inert pin report success, the precise failure this guard exists to catch.
//
// The declared name is the right thing to look for even when the overlay
// previously rewrote the repository with newName: `kustomize edit set image
// <name>@<digest>` replaces the whole images[] entry, dropping any newName, so
// the render always carries the declared repository afterwards.
func assertPinsRendered(rendered []byte, pins []Pin) error {
	text := string(rendered)
	for _, pin := range pins {
		if strings.Contains(text, pin.Ref()) {
			continue
		}
		return fmt.Errorf("pinning %s had no effect: %s does not appear in the rendered output.\nDoes the base reference %s? kustomize adds an images[] entry that matches nothing without applying it",
			pin.Image, pin.Ref(), pin.Image)
	}
	return nil
}

// Revert discards the overlay edits. Callers use it when they decide not to
// commit after building a plan.
func (p *Plan) Revert(ctx context.Context, repo *gitops.Repository) error {
	return repo.Git.CheckoutPaths(ctx, p.Coordinate.OverlayRelPath(p.Environment))
}

// RelKustomizationPath returns the modified file relative to the repository
// root, for staging.
func (p *Plan) RelKustomizationPath(root string) string {
	rel, err := filepath.Rel(root, p.KustomizationPath)
	if err != nil {
		return p.KustomizationPath
	}
	return filepath.ToSlash(rel)
}

// withRevert reverts the overlay and reports both failures if the revert also
// fails — leaving a dirty shared checkout silently would poison the next run.
func withRevert(cause error, revert func() error) error {
	if err := revert(); err != nil {
		return fmt.Errorf("%w (and the overlay could not be reverted: %v)", cause, err)
	}
	return cause
}
