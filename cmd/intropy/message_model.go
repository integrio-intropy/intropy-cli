package main

// The shared message model behind `message list` and `message show`: one
// shape for registry-served messages and workspace-declared ones, so the
// human table, the JSON document, and the show detail cannot drift apart.

// MessageEntry is one message as the message commands emit it, in JSON
// output. Source marks where the entry came from: "registry" (external,
// with its producing channels and schema pin) or "workspace" (internal, a
// publishes declaration in a scaffold record).
type MessageEntry struct {
	Message string `json:"message"`
	Source  string `json:"source"`

	Group          string     `json:"group,omitempty"`
	Type           string     `json:"type,omitempty"`
	Envelope       string     `json:"envelope,omitempty"`
	Channels       []string   `json:"channels,omitempty"` // <pubsub>/<topic>
	DataSchema     string     `json:"dataschema,omitempty"`
	DataSchemaURL  string     `json:"dataschemaurl,omitempty"`
	DataSchemaName string     `json:"dataschemaname,omitempty"`
	Contract       string     `json:"contract,omitempty"`
	Path           string     `json:"path,omitempty"` // workspace: the record's project directory
	EnvelopeMeta   []kvHolder `json:"envelopeMetadata,omitempty"`
}

// kvHolder is one envelope metadata attribute as JSON output: attribute
// name, value, and whether the envelope requires it.
type kvHolder struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Required bool   `json:"required,omitempty"`
}
