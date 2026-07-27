package deploy

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

// ResultPin is one image's before and after state.
type ResultPin struct {
	Image    string `json:"image"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`

	// Tag is the tag the digest was resolved from. Empty when the digest came
	// from a release manifest, which records digests rather than tags.
	Tag string `json:"tag,omitempty"`
}
