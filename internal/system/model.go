package system

import "github.com/integrio-intropy/intropy-cli/internal/template"

// TopicKey identifies a topic within the system: the pub/sub component it
// lives on plus the topic name. Two scaffolds naming the same key declare
// the two halves of one contract. It aliases the canonical type in
// internal/template — there is one "which topic" concept across the CLI,
// shared by assembly and prompt-time facts.
type TopicKey = template.TopicKey

// Topic is one assembled topic: its key and the C# contract type its
// messages carry. The identifier it gets in the generated Topics class is
// derived by the template, not the CLI.
type Topic struct {
	TopicKey
	Contract string `json:"contract"`
}

// The two directions a message wiring can take. Publish/subscribe come
// from the record's block when it has one, and from the block kind for
// legacy records (an extractor publishes, a loader subscribes) — the same
// rule the host template applies today.
const (
	MessagePublish   = "publish"
	MessageSubscribe = "subscribe"
)

// Message is one message declaration in the system's internal messagegroup.
// Name doubles as the CloudEvents type for internal messages; Contract is
// the transitional .NET shared-project type; Dataschema is the logical
// schema reference for registry-subscribed messages, empty for internal
// ones.
type Message struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Contract   string `json:"contract,omitempty"`
	Dataschema string `json:"dataschema,omitempty"`
	Publisher  string `json:"publisher,omitempty"`
	Subscriber string `json:"subscriber,omitempty"`
}

// Component is one assembled system block. Its wiring is shape-driven:
// Topic is nil for kinds without one, and Ports carries the named ports in
// the kind's order (From before To for transactional blocks).
//
// Topic subsumes the message channel: a legacy record pairs on its flat
// topic keys, and a block-shaped record gets its channel resolved during
// assembly — an external subscribe block carries its own snapshot, an
// internal subscription takes the producer's, and a producer without a
// recorded topic defaults to the system pubsub on a topic named after the
// message. Every component therefore renders against concrete transport
// wiring even in a fully message-shaped workspace.
type Component struct {
	AppID string    `json:"name"` // the Add<Kind> argument in the generated system class
	Kind  string    `json:"kind"` // a key of the blockParsers registry
	Topic *TopicKey `json:"-"`    // nil for kinds without a topic
	// Port is the single port of a topic block; empty for records that
	// predate it. Kept alongside Ports so the --output-json summary stays
	// additive-only.
	Port  string   `json:"port,omitempty"`
	Ports []string `json:"ports,omitempty"` // transactional blocks: exactly [from, to]
	Path  string   `json:"path"`            // scaffold directory, for error messages

	// Message carries the component's message wiring: what it publishes or
	// subscribes by name, and whether a subscription is external (its own
	// channel snapshot) or internal (resolved against a producer here).
	Message *MessageWiring

	// topicContract is the contract type of Topic, carried on the
	// component because topics dedupe across components: the model's
	// Topics list needs one contract per key, first seen wins.
	topicContract string
	// missingPort marks a topic-block record that predates the port value,
	// so Assemble can warn without failing the record.
	missingPort bool
}

// MessageWiring is the message-view wiring of one component.
type MessageWiring struct {
	Name string
	Kind string // MessagePublish or MessageSubscribe

	// Contract is the transitional .NET shared-project type recorded
	// alongside the message name; empty when the template no longer
	// records one.
	Contract string

	// Dataschema carries the logical schema xid of an external
	// subscription's snapshot; internal declarations have none.
	Dataschema string

	// External marks a subscription whose record carries its own channel
	// snapshot: it assembles without a producer and never contacts the
	// registry (assembly stays offline by construction).
	External bool
}

// Port is one assembled port: the named edge a block reaches the outside
// world through. The declaration carries only the deployed transport shape;
// `sys create` resolves every port to a folder under the host's test/
// directory through the generated development definition. The identifier it
// gets in the generated Ports class is derived by the template, not the CLI.
type Port struct {
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
	Topics      []Topic   // sorted by (Pubsub, Name)
	Messages    []Message // sorted by Name; the internal messagegroup
	Ports       []Port    // sorted by Name
	// Shared is the workspace's shared-library scaffold, nil when none
	// exists — valid for a topic-free system, and for a topic-bearing one
	// the host template scaffolds the contracts project as a dependency.
	Shared *SharedLibrary
}
