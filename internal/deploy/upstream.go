package deploy

import (
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// Upstream statuses. All four are informational: none of them can fail a
// deployment.
const (
	// UpstreamMatch means every image is pinned there to the digest being
	// deployed here.
	UpstreamMatch = "match"

	// UpstreamDiffers means it pins a digest, but a different one.
	UpstreamDiffers = "differs"

	// UpstreamUnpinned means it pins a tag, or nothing, for some image — so
	// there is no digest to compare.
	UpstreamUnpinned = "unpinned"

	// UpstreamUnknown means it could not be read: the component is not
	// onboarded there, or its overlay is unreadable.
	UpstreamUnknown = "unknown"
)

// Upstream is what an environment this one promotes from currently has pinned.
type Upstream struct {
	Environment string `json:"environment"`
	Status      string `json:"status"`

	// Pinned is how that overlay pins each image, keyed by image name, in the
	// same form as Plan.Previous. A missing key means the overlay has no entry
	// for that image.
	Pinned map[string]string `json:"pinned,omitempty"`

	// SourceCommit is the deploy.internal/source-commit annotation that
	// overlay carries, empty when it has none.
	SourceCommit string `json:"sourceCommit,omitempty"`
}

// InspectUpstreams reads what each environment named by the target's
// promotesFrom currently has pinned, and compares it with the digests about to
// be deployed.
//
// Informational, and deliberately so: promotion policy — refusing a staging
// deploy of bits dev never ran, honouring requireSourceHealthy — belongs to the
// promote command. A deploy that *says* "these are not the bits dev tested" is
// more useful than one that refuses to run.
//
// Nothing here can fail. An environment with no overlay for this component, an
// unreadable kustomization, or the target naming itself yields a note or
// nothing at all.
func InspectUpstreams(root string, coord gitops.Coordinate, comp *gitops.ComponentConfig, target string, env gitops.EnvironmentConfig, pins []source.Pin) []Upstream {
	var out []Upstream
	for _, name := range env.PromotesFrom {
		// An environment that promotes from itself would compare the overlay
		// with the edit about to be made to it. deploy.yaml does not forbid it.
		if name == target {
			continue
		}
		out = append(out, inspectUpstream(root, coord, comp, name, pins))
	}
	return out
}

func inspectUpstream(root string, coord gitops.Coordinate, comp *gitops.ComponentConfig, upstream string, pins []source.Pin) Upstream {
	u := Upstream{Environment: upstream, Status: UpstreamUnknown}

	dir, err := gitops.ResolveOverlay(root, coord, comp, upstream)
	if err != nil {
		return u
	}
	k, _, err := kustomize.ReadKustomization(dir)
	if err != nil {
		return u
	}

	u.SourceCommit = k.CommonAnnotations[kustomize.AnnotationSourceCommit]
	u.Pinned = make(map[string]string, len(pins))

	status := UpstreamMatch
	for _, pin := range pins {
		img, found := k.FindImage(pin.Image)
		if !found || img.Digest == "" {
			if found {
				u.Pinned[pin.Image] = img.Pinned()
			}
			// Unpinned outranks differs: "there is nothing to compare" is the
			// more accurate thing to say about the whole environment.
			status = UpstreamUnpinned
			break
		}
		u.Pinned[pin.Image] = img.Digest
		if img.Digest != pin.Digest {
			status = UpstreamDiffers
		}
	}
	u.Status = status
	return u
}

// Describe renders the one-line reassurance, or the one-line qualification.
func (u Upstream) Describe(pins []source.Pin) string {
	switch u.Status {
	case UpstreamMatch:
		subject := "these digests"
		if len(pins) == 1 {
			subject = "this digest"
		}
		if u.SourceCommit != "" {
			return fmt.Sprintf("%s already runs %s (commit %s) — you are shipping the tested bits",
				u.Environment, subject, git.ShortSHA(u.SourceCommit))
		}
		return fmt.Sprintf("%s already runs %s — you are shipping the tested bits", u.Environment, subject)

	case UpstreamDiffers:
		image, digest := u.firstDisagreement(pins)
		return fmt.Sprintf("%s runs a different digest for %s (%s) — these bits have not run there",
			u.Environment, image, shortDigest(digest))

	case UpstreamUnpinned:
		image, _ := u.firstDisagreement(pins)
		return fmt.Sprintf("%s pins no digest for %s, so there is nothing to compare", u.Environment, image)

	default:
		return fmt.Sprintf("%s could not be read, so there is nothing to compare", u.Environment)
	}
}

// firstDisagreement names the first image, in declared order, that the upstream
// environment does not pin to the digest being deployed. Declared order keeps
// the message stable across runs.
func (u Upstream) firstDisagreement(pins []source.Pin) (image, digest string) {
	for _, pin := range pins {
		there, found := u.Pinned[pin.Image]
		if !found || there != pin.Digest {
			return pin.Image, there
		}
	}
	// Unreachable for differs and unpinned, but a plausible message beats an
	// empty one if a caller ever asks about a match.
	if len(pins) > 0 {
		return pins[0].Image, u.Pinned[pins[0].Image]
	}
	return "", ""
}
