package system

// TopicKey identifies a topic within the system: the pub/sub component it
// lives on plus the topic name. Two scaffolds naming the same key declare
// the two halves of one contract.
type TopicKey struct {
	Pubsub string `json:"pubsub"`
	Name   string `json:"topic"`
}

// Topic is one assembled topic: its key and the C# contract type its
// messages carry. The identifier it gets in the generated Topics class is
// derived by the template, not the CLI.
type Topic struct {
	TopicKey
	Contract string `json:"contract"`
}

// Component is one assembled system block. Its wiring is shape-driven:
// Topic is nil for kinds without one, and Connectors carries the named
// ports in the kind's order (From before To for transactional blocks).
type Component struct {
	AppID string    `json:"name"` // the Add<Kind> argument in the generated system class
	Kind  string    `json:"kind"` // a key of the blockParsers registry
	Topic *TopicKey `json:"-"`    // nil for kinds without a topic
	// Connector is the single port of a topic block; empty for records
	// that predate it. Kept alongside Connectors so the --output-json
	// summary stays additive-only.
	Connector  string   `json:"connector,omitempty"`
	Connectors []string `json:"connectors,omitempty"` // transactional blocks: exactly [from, to]
	Path       string   `json:"path"`                 // scaffold directory, for error messages

	// topicContract is the contract type of Topic, carried on the
	// component because topics dedupe across components: the model's
	// Topics list needs one contract per key, first seen wins.
	topicContract string
	// missingConnector marks a topic-block record that predates the
	// connector value, so Assemble can warn without failing the record.
	missingConnector bool
}

// Connector is one assembled connector: the named port an edge block reaches
// the outside world through. The declaration carries only the deployed
// transport shape; `sys create` resolves every connector to a folder under
// the host's test/ directory through the generated development definition.
// The identifier it gets in the generated Connectors class is derived by the
// template, not the CLI.
type Connector struct {
	Name string `json:"name"`
}

// SharedLibrary is the workspace's contract project: referenced by the
// host, never declared as a component.
type SharedLibrary struct {
	Path string // scaffold directory
	Name string // project name; also the namespace and csproj basename
}

// Model is the assembled system declaration `sys create` generates code
// from. Name, ProjectName and SystemClass are filled in around the
// template render; Assemble leaves them empty.
type Model struct {
	Name        string // kebab-case system name
	ProjectName string // template-derived PascalCase name
	SystemClass string // template-derived ISystemDefinition class name
	Components  []Component
	Topics      []Topic     // sorted by (Pubsub, Name)
	Connectors  []Connector // sorted by Name
	// Shared is the workspace's shared-library scaffold, nil when none
	// exists — valid for a topic-free system, and for a topic-bearing one
	// the host template scaffolds the contracts project as a dependency.
	Shared *SharedLibrary
}
