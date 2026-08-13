// Package topology models the declared system topology an integration
// system's host emits. The record declares the system's shape: its components
// (blocks) and the wiring between them, expressed inline on each component —
// the topics it subscribes to and publishes on, the API contracts it provides
// and consumes, and the external connectors it uses. Top-level topics[] and
// connectors[] sections carry the shared metadata (contracts, external
// systems) those inline references point at.
//
// The record carries only minted facts: the Dapr binding a connector deploys
// as (its spec.type, address, credentials) is environment-owned deployment
// configuration in the GitOps repo, and an extractor's cadence is its CronJob
// there. Neither appears here.
//
// The CLI never derives a topology itself: the system host's `graph` verb
// prints the record — JSON only, on stdout — and consumers decode that stream.
package topology

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	// APIVersion is the topology schema this CLI understands. A record with
	// any other apiVersion is an error, not a guess.
	APIVersion = "topology.intropy.io/v1"

	// Kind is the expected document kind under APIVersion.
	Kind = "SystemTopology"
)

// Topology is one system's declared graph. Wiring is inlined on each component
// rather than kept in a separate edge list: a component names the topics it
// subscribes to and publishes on and the connectors it uses. The top-level
// Topics and Connectors sections are lookup tables the inline references
// resolve against (contract, external system).
type Topology struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind,omitempty"`
	System     string      `json:"system"`
	Components []Component `json:"components,omitempty"`
	Topics     []Topic     `json:"topics,omitempty"`
	Connectors []Connector `json:"connectors,omitempty"`
	Contracts  []Contract  `json:"contracts,omitempty"`
	// APIs, and each component's Provides/Consumes below, are the contract
	// (request/response) surfaces. Their element shape is not yet finalized,
	// so they are parsed opaquely: preserved for round-tripping to the
	// frontend without asserting a schema the CLI does not yet render.
	APIs []json.RawMessage `json:"apis,omitempty"`
}

// Component is a deployable block of the system. Kind is the block type
// (extractor, loader, transactional-integration, …). A component's directory,
// which joins it to its .intropy/scaffold.json project, is its Name relative
// to the system root.
type Component struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Subscribes []TopicRef     `json:"subscribes,omitempty"`
	Publishes  []Publication  `json:"publishes,omitempty"`
	Connectors []ConnectorUse `json:"connectors,omitempty"`
	// Provides/Consumes are contract surfaces, parsed opaquely (see APIs).
	Provides []json.RawMessage `json:"provides,omitempty"`
	Consumes []json.RawMessage `json:"consumes,omitempty"`
}

// TopicRef is a component's subscription to a pub/sub topic.
type TopicRef struct {
	PubSub string `json:"pubsub"`
	Topic  string `json:"topic"`
}

// Publication is a component's output onto a pub/sub topic. Port names the
// component output the message leaves through.
type Publication struct {
	Port   string `json:"port,omitempty"`
	PubSub string `json:"pubsub"`
	Topic  string `json:"topic"`
}

// ConnectorUse is a component's use of an external connector. Direction is
// "in" (external → component) or "out" (component → external).
type ConnectorUse struct {
	Connector string `json:"connector"`
	Direction string `json:"direction"`
}

// Topic is a declared pub/sub topic: the metadata for a (PubSub, Topic) pair
// components reference. Contract is the message contract it carries;
// Publishers/Subscribers name the components on each end.
type Topic struct {
	PubSub      string   `json:"pubsub"`
	Topic       string   `json:"topic"`
	Contract    string   `json:"contract,omitempty"`
	Publishers  []string `json:"publishers,omitempty"`
	Subscribers []string `json:"subscribers,omitempty"`
}

// Contract is a message contract in the system's registry, keyed by Name —
// the same fully-qualified type name Topic.Contract references. ShortName is
// the bare type name (the join key to a scaffold record's values.contract).
// Schema is the contract's JSON Schema as the host emitted it; the CLI passes
// it through to the frontend without interpreting it. Fingerprint identifies
// the schema shape (a hash over its canonical form), so equal fingerprints
// mean equal shapes across systems.
type Contract struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind,omitempty"`
	ShortName   string          `json:"shortName,omitempty"`
	MediaType   string          `json:"mediaType,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// Connector is an external system integration point. The name is its whole
// identity — the deployed Dapr binding type, address, and credentials are
// environment-owned deployment configuration, never part of the record.
// ExternalSystem is the system it fronts, Directions the directions it is
// wired in, and UsedBy the components that use it.
type Connector struct {
	Name           string   `json:"name"`
	ExternalSystem string   `json:"externalSystem,omitempty"`
	Directions     []string `json:"directions,omitempty"`
	UsedBy         []string `json:"usedBy,omitempty"`
}

// Entry is one system's topology plus the workspace directory it belongs to,
// flattened into one self-describing JSON document for the API.
type Entry struct {
	Path string `json:"path"`
	Topology
}

// Decode parses a topology record from r (typically a host `graph` verb's
// stdout). A record whose apiVersion this CLI does not understand is an
// error, not a guess. Callers wrap the error with the record's origin.
func Decode(r io.Reader) (*Topology, error) {
	var t Topology
	if err := json.NewDecoder(r).Decode(&t); err != nil {
		return nil, fmt.Errorf("parse topology record: %w", err)
	}
	if t.APIVersion != APIVersion {
		return nil, fmt.Errorf("unsupported topology apiVersion %q (this CLI understands %q)",
			t.APIVersion, APIVersion)
	}
	return &t, nil
}
