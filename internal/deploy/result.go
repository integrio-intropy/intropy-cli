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

	SyncPolicy string `json:"syncPolicy"`

	// SyncStatus, HealthStatus and SyncedRevision are what ArgoCD reported once
	// it converged. Empty when the wait was skipped, the environment syncs
	// manually, or ArgoCD could not be reached — none of which means the
	// deployment failed.
	SyncStatus     string `json:"syncStatus,omitempty"`
	HealthStatus   string `json:"healthStatus,omitempty"`
	SyncedRevision string `json:"syncedRevision,omitempty"`
}

// ResultPin is one image's before and after state.
type ResultPin struct {
	Image    string `json:"image"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`
	Tag      string `json:"tag"`
}
