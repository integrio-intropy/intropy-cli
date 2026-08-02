package system

// TopicKey identifies a topic within the system: the pub/sub component it
// lives on plus the topic name. Two scaffolds naming the same key declare
// the two halves of one contract.
type TopicKey struct {
	Pubsub string `json:"pubsub"`
	Name   string `json:"topic"`
}

// Topic is one assembled topic: its key, the C# contract type its messages
// carry, and the identifier it gets in the generated Topics class.
type Topic struct {
	TopicKey
	Contract string `json:"contract"`
	Field    string `json:"-"`
}

// Component is one assembled system block.
type Component struct {
	AppID     string   `json:"name"` // the AddExtractor/AddLoader argument
	Kind      string   `json:"kind"` // template.BlockKindExtractor or BlockKindLoader
	Topic     TopicKey `json:"-"`
	Connector string   `json:"connector,omitempty"` // the From/To port; empty for records that predate it
	Path      string   `json:"path"`                // scaffold directory, for error messages
}

// Connector is one assembled connector: the named port an edge block reaches
// the outside world through, and the identifier it gets in the generated
// Connectors class. The declaration carries only the deployed transport
// shape; `sys create` resolves every connector to a folder under the host's
// test/ directory through the generated development definition.
type Connector struct {
	Name  string `json:"name"`
	Field string `json:"-"`
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
	Shared      SharedLibrary
}
