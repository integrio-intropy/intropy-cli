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
	Applied      bool        `json:"applied"`
	SyncPolicy   string      `json:"syncPolicy"`
}

// ResultPin is one image's before and after state.
type ResultPin struct {
	Image    string `json:"image"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`
	Tag      string `json:"tag"`
}
