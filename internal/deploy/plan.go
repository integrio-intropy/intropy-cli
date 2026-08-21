package deploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// Plan is the outcome of editing an overlay and comparing the render before and
// after. It is produced without writing to git, so it is safe to build and
// discard.
type Plan struct {
	Coordinate  gitops.Coordinate
	Environment string
	Source      source.State

	// ReleaseVersion is the version the pins came from, empty when the digests
	// were resolved from the source repository's HEAD.
	//
	// A version rather than the manifest: nothing here needs more than the
	// version, and a promotion learns it from the source overlay's annotation
	// rather than from a manifest it never reads.
	ReleaseVersion string

	// PromotedFrom is the environment the digests were copied from, empty for a
	// deployment. A promotion resolves nothing — it copies — so this records
	// where the bits came from in place of a registry lookup.
	PromotedFrom string

	// Pins are the digests resolved for this commit.
	Pins []source.Pin

	// Previous records how each image was pinned before the edit, keyed by
	// image name, for a readable summary.
	Previous map[string]string

	// PreviousRelease is the version the target overlay was pinned from before
	// the edit, empty when it carried no release annotation.
	PreviousRelease string

	// Upstreams is what each environment this one promotes from currently has
	// pinned. Nil when the environment promotes from nothing. Informational: a
	// mismatch is reported, never enforced.
	Upstreams []Upstream

	// Notes are extra lines to print under the summary, already phrased. A
	// promotion uses them to say where the digests came from, which it knows
	// exactly and so has no upstream comparison to make.
	Notes []string

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

// Summary renders the one-line-per-image human summary, followed by one line
// per environment this one promotes from.
func (p *Plan) Summary() string {
	var b strings.Builder
	if p.ReleaseVersion != "" {
		fmt.Fprintf(&b, "%s → %s (release %s, commit %s)\n", p.Coordinate, p.Environment, p.ReleaseVersion, p.Source.ShortCommit())
	} else {
		fmt.Fprintf(&b, "%s → %s (commit %s)\n", p.Coordinate, p.Environment, p.Source.ShortCommit())
	}
	for _, pin := range p.Pins {
		was := p.Previous[pin.Image]
		if was == "" {
			was = "(absent)"
		}
		fmt.Fprintf(&b, "  %s\n    %s → %s\n", pin.Image, was, pin.Digest)
	}
	b.WriteString(p.noteLines())
	return b.String()
}

// noteLines renders the plan's own notes and the promotesFrom comparison,
// indented to sit under the summary. Empty when there is neither — silence is
// the right output for an environment with no upstream.
func (p *Plan) noteLines() string {
	var b strings.Builder
	for _, note := range p.Notes {
		fmt.Fprintf(&b, "  %s\n", note)
	}
	for _, u := range p.Upstreams {
		fmt.Fprintf(&b, "  %s\n", u.Describe(p.Pins))
	}
	return b.String()
}

// pinnedAs names what the environment is already at, for the no-op message: a
// release version when there is one, otherwise the digest.
//
// Pins is never empty — component.yaml requires at least one image and both
// resolvers emit one pin per declared image — which is what makes the index
// safe here and in commitSubject.
func (p *Plan) pinnedAs() string {
	if p.ReleaseVersion != "" {
		return "release " + p.ReleaseVersion
	}
	return p.Pins[0].Digest
}

// PlanOptions configures BuildPlan.
type PlanOptions struct {
	Repository  *gitops.Repository
	Kustomize   kustomize.Client
	Coordinate  gitops.Coordinate
	Environment string
	Source      source.State

	// ReleaseVersion is annotated onto the overlay. Empty means the digests
	// came from a commit, and any release annotation already there is removed.
	ReleaseVersion string

	// PromotedFrom is the environment the digests were copied from, empty for a
	// deployment.
	PromotedFrom string

	Pins      []source.Pin
	Upstreams []Upstream

	// Notes are extra summary lines, already phrased.
	Notes []string

	OverlayDir string
	Palette    kustomize.Palette
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
		ReleaseVersion:    opts.ReleaseVersion,
		PromotedFrom:      opts.PromotedFrom,
		Pins:              opts.Pins,
		Previous:          previous,
		PreviousRelease:   current.CommonAnnotations[kustomize.AnnotationRelease],
		Upstreams:         opts.Upstreams,
		Notes:             opts.Notes,
		DigestChanged:     digestChanged,
		OverlayDir:        opts.OverlayDir,
		KustomizationPath: kustPath,
	}

	revert := func() error {
		return opts.Repository.Git.CheckoutPaths(ctx, overlayRel)
	}

	if err := applyEdits(ctx, opts, current); err != nil {
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

// applyEdits pins every digest and records where they came from.
//
// current is the overlay as it was before the edit, used only to decide whether
// there is a release annotation to remove.
func applyEdits(ctx context.Context, opts PlanOptions, current *kustomize.Kustomization) error {
	for _, pin := range opts.Pins {
		if err := opts.Kustomize.SetImage(ctx, opts.OverlayDir, pin.Image, pin.Digest); err != nil {
			return err
		}
	}
	if err := opts.Kustomize.SetAnnotation(ctx, opts.OverlayDir, kustomize.AnnotationSourceCommit, opts.Source.Commit); err != nil {
		return err
	}

	if opts.ReleaseVersion != "" {
		return opts.Kustomize.SetAnnotation(ctx, opts.OverlayDir, kustomize.AnnotationRelease, opts.ReleaseVersion)
	}
	// These digests came from a commit, so any version recorded here describes a
	// deployment that is being replaced. Leaving it would be worse than having
	// no annotation at all: a stale version beside an unrelated digest is read
	// as fact by promote, and would promote a version prod never ran.
	if _, found := current.CommonAnnotations[kustomize.AnnotationRelease]; found {
		return opts.Kustomize.RemoveAnnotation(ctx, opts.OverlayDir, kustomize.AnnotationRelease)
	}
	return nil
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
func assertPinsRendered(rendered []byte, pins []source.Pin) error {
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
