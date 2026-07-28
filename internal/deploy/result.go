package deploy

import "time"

// Result is the machine-readable outcome. Field names are stable and
// additive-only.
type Result struct {
	Component    string      `json:"component"`
	Domain       string      `json:"domain"`
	System       string      `json:"system"`
	Environment  string      `json:"environment"`
	SourceCommit string      `json:"sourceCommit"`
	AppName      string      `json:"appName"`
	OverlayPath  string      `json:"overlayPath"`
	Pins         []ResultPin `json:"pins"`
	Changed      bool        `json:"changed"`

	// Applied reports whether the change was committed and pushed.
	Applied bool `json:"applied"`

	// Revision is the sha that landed on the GitOps branch. Empty unless
	// Applied. It is the post-rebase sha, which is what ArgoCD reports back.
	Revision string `json:"revision,omitempty"`

	// Release is the version deployed. Empty when the digests were resolved
	// from the source repository's HEAD.
	Release string `json:"release,omitempty"`

	// PromotedFrom is the environment the digests were copied from. Present only
	// for a promotion, which resolves nothing and so has no other record of
	// where its digests came from.
	PromotedFrom string `json:"promotedFrom,omitempty"`

	// Upstreams reports what the environments this one promotes from currently
	// have pinned, so a consumer can see whether these are the bits that were
	// tested upstream. Informational: a mismatch never fails the deploy.
	Upstreams []Upstream `json:"upstreams,omitempty"`

	SyncPolicy string `json:"syncPolicy"`

	// SyncStatus, HealthStatus and SyncedRevision are what ArgoCD reported once
	// it converged. Empty when the wait was skipped, the environment syncs
	// manually, or ArgoCD could not be reached — none of which means the
	// deployment failed.
	SyncStatus     string `json:"syncStatus,omitempty"`
	HealthStatus   string `json:"healthStatus,omitempty"`
	SyncedRevision string `json:"syncedRevision,omitempty"`
}

// SyncResult is the machine-readable outcome of a sync. Its own type rather than
// a Result: a sync pins nothing and plans nothing, so most of Result's fields
// would be permanently empty.
type SyncResult struct {
	Component   string `json:"component"`
	Domain      string `json:"domain"`
	System      string `json:"system"`
	Environment string `json:"environment"`
	AppName     string `json:"appName"`
	OverlayPath string `json:"overlayPath"`

	// Revision is the commit that was synced: the one that last changed the
	// overlay, which is what was reviewed.
	Revision string `json:"revision"`

	// Requested reports whether a sync was actually asked for. False means
	// ArgoCD already held that revision and was left alone.
	Requested bool `json:"requested"`

	SyncPolicy string `json:"syncPolicy"`

	// SyncStatus, HealthStatus and SyncedRevision are what ArgoCD reported once
	// it converged. Empty when --no-wait was given or ArgoCD became unreachable
	// after the sync was accepted.
	SyncStatus     string `json:"syncStatus,omitempty"`
	HealthStatus   string `json:"healthStatus,omitempty"`
	SyncedRevision string `json:"syncedRevision,omitempty"`
}

// DiffResult is the machine-readable outcome of a diff. Its own type for the same
// reason SyncResult is: nothing here is pinned, planned or applied.
type DiffResult struct {
	Component   string `json:"component"`
	Domain      string `json:"domain"`
	System      string `json:"system"`
	Environment string `json:"environment"`
	AppName     string `json:"appName"`
	OverlayPath string `json:"overlayPath"`

	// Pending is the full sha a sync would apply — full rather than abbreviated
	// because it is meant to be handed straight to `sync --revision`, whose
	// comparison is a prefix match that an abbreviation only weakens.
	Pending string `json:"pending"`

	// Synced is the revision ArgoCD reports it has applied, and the baseline the
	// diff is against. Empty when the application has never been synced, in which
	// case every resource below is new.
	Synced string `json:"synced,omitempty"`

	// Applied reports that ArgoCD already holds the pending revision, or a
	// descendant of it.
	Applied bool `json:"applied"`

	// Changed reports whether the two renders differ at all.
	Changed bool `json:"changed"`

	// Diff is the unified diff of the two renders, never coloured in this form.
	Diff string `json:"diff,omitempty"`

	// RemovedResources are the resources the baseline renders and the pending
	// revision does not. They will not leave the cluster: a sync from here does
	// not prune.
	RemovedResources []string `json:"removedResources,omitempty"`

	// Subject, Release, PromotedFrom, SourceCommit and DeployedBy are what the
	// pending commit says about itself, read from its trailers. An approver's
	// other question is who asked for this, and from what.
	Subject      string `json:"subject,omitempty"`
	Release      string `json:"release,omitempty"`
	PromotedFrom string `json:"promotedFrom,omitempty"`
	SourceCommit string `json:"sourceCommit,omitempty"`
	DeployedBy   string `json:"deployedBy,omitempty"`

	SyncPolicy   string `json:"syncPolicy"`
	SyncStatus   string `json:"syncStatus,omitempty"`
	HealthStatus string `json:"healthStatus,omitempty"`
}

// StatusResult is the machine-readable outcome of a status. Its own type for
// the same reason SyncResult is: it plans nothing, applies nothing, and is the
// only result here that describes more than one environment.
// InitResult is the machine-readable summary of a deploy init run.
//
// Placeholders is the point of the document: complete manifests are explicitly
// not the goal, so what a consumer wants to know is exactly which values a human
// still has to supply and where.
type InitResult struct {
	System string `json:"system"`
	Domain string `json:"domain"`

	// Host is the component directory holding the system's shared manifests.
	Host string `json:"host"`

	// Template is the resolved template library reference, so a reviewer can tell
	// which release produced the tree.
	Template string `json:"template"`

	Components []string `json:"components"`

	// Files is every staged path and what was done with it, including the ones
	// left alone.
	Files []FileAction `json:"files"`

	// Applied is false for --plan and for a run that found nothing to write.
	Applied  bool   `json:"applied"`
	Branch   string `json:"branch,omitempty"`
	Revision string `json:"revision,omitempty"`

	Placeholders []Placeholder `json:"placeholders"`
}

type StatusResult struct {
	Component string `json:"component"`
	Domain    string `json:"domain"`
	System    string `json:"system"`

	// Kind is the component's declared kind, always populated even when
	// component.yaml leaves it implicit. A consumer reading empty digests needs
	// to tell "shared, so there is nothing to pin" from "never deployed".
	Kind string `json:"kind"`

	// Environments are in promotion order, so the last one is the furthest
	// downstream — usually production.
	Environments []EnvironmentStatus `json:"environments"`

	// Consistent reports that every onboarded environment pins the identical
	// digest for every image the component declares. This is the question the
	// command exists to answer: promotion copies digests rather than rebuilding,
	// so agreement here is what makes "prod runs the bits staging tested" true.
	//
	// False when any environment disagrees, pins a tag instead of a digest, or
	// could not be read — in none of those cases has agreement been shown.
	Consistent bool `json:"consistent"`

	// Summary is the sentence the plain output prints beneath its table: what
	// the environments collectively run, or why they cannot be compared.
	//
	// It travels in the result because Consistent is a bool and the interesting
	// cases are not: "these two run different bits" and "there is nothing to
	// compare here" are both false and mean very different things. A second
	// presenter deriving its own sentence from Environments would eventually
	// draw a different conclusion from the same rows than this command does.
	Summary string `json:"summary"`

	// Notes are the qualifications under that sentence: an environment that
	// could not be read, one that pins a tag rather than a digest, one waiting
	// on a sync gate. Empty when there is nothing to qualify.
	//
	// The plain output adds one more note that is about its own table rather
	// than about the deployment, and so is deliberately absent here.
	Notes []string `json:"notes,omitempty"`
}

// EnvironmentStatus is one environment's row.
type EnvironmentStatus struct {
	Environment string `json:"environment"`
	AppName     string `json:"appName"`
	OverlayPath string `json:"overlayPath"`

	// Onboarded reports that the component has a readable overlay here. False
	// leaves everything below it empty, and Reason says why.
	Onboarded bool   `json:"onboarded"`
	Reason    string `json:"reason,omitempty"`

	// Release and SourceCommit are the deploy.internal/release and
	// deploy.internal/source-commit annotations. Release is empty when the
	// environment was deployed from a commit rather than a release.
	Release      string `json:"release,omitempty"`
	SourceCommit string `json:"sourceCommit,omitempty"`

	// Pins is every image the component declares, in that order — not just the
	// one the table has room for. Digest is empty when the overlay pins a tag
	// or nothing, in which case Tag says which.
	Pins []ResultPin `json:"pins,omitempty"`

	// Revision is the GitOps commit that last changed this overlay, and
	// DeployedAt when it landed. Both empty when the path has no history.
	Revision   string     `json:"revision,omitempty"`
	DeployedAt *time.Time `json:"deployedAt,omitempty"`

	SyncPolicy string `json:"syncPolicy"`

	// SyncStatus, HealthStatus and SyncedRevision are what ArgoCD reports.
	// Empty when it could not be reached or does not know this application —
	// neither of which says anything about what the overlay pins.
	SyncStatus     string `json:"syncStatus,omitempty"`
	HealthStatus   string `json:"healthStatus,omitempty"`
	SyncedRevision string `json:"syncedRevision,omitempty"`

	// Pending reports a committed overlay change ArgoCD has not applied. For a
	// manual-sync environment that is the normal resting state of an unspent
	// gate, not a fault.
	Pending bool `json:"pending"`
}

// ResultPin is one image's before and after state.
type ResultPin struct {
	Image    string `json:"image"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`

	// Tag is the tag the digest was resolved from. Empty when the digest came
	// from a release manifest, which records digests rather than tags.
	Tag string `json:"tag,omitempty"`
}
