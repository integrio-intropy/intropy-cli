// Package topology models the declared system topology an integration
// system's host emits. The record declares the system's shape: its components
// (blocks) and the wiring between them, expressed inline on each component —
// the topics it subscribes to and publishes on, the API contracts it provides
// and consumes, and the external ports it uses. Top-level topics[] and
// ports[] sections carry the shared metadata (contracts, external systems)
// those inline references point at.
//
// The record carries only minted facts: the Dapr binding a port deploys as
// (its spec.type, address, credentials) is environment-owned deployment
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
// subscribes to and publishes on and the ports it uses. The top-level Topics
// and Ports sections are lookup tables the inline references resolve against
// (contract, external system).
type Topology struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind,omitempty"`
	System     string      `json:"system"`
	Components []Component `json:"components,omitempty"`
	Topics     []Topic     `json:"topics,omitempty"`
	Ports      []Port      `json:"ports,omitempty"`
	Contracts  []Contract  `json:"contracts,omitempty"`
	// APIs, each component's Provides/Consumes below, and MessageGroups
	// are sections whose element shape is not yet finalized, so they are
	// parsed opaquely: preserved for round-tripping to the frontend
	// without asserting a schema the CLI does not yet render.
	APIs []json.RawMessage `json:"apis,omitempty"`
	// MessageGroups is the system's internal messagegroup section, emitted
	// by hosts new enough to model messages by name. A nil section on an
	// older host decodes identically to today.
	MessageGroups []json.RawMessage `json:"messagegroups,omitempty"`
	// Development is the host's local-run picture, present only when the
	// graph verb ran with --development against a host whose Intropy.Topology
	// is new enough to emit it. A nil Development means "no dev configuration
	// known" — older hosts, hosts without a development definition, and plain
	// graph runs all decode identically.
	Development *Development `json:"development,omitempty"`
}

// Development is the development section of a topology record: the host
// author's local substitutions, emitted by `graph --development`.
type Development struct {
	// Files maps an external port to the local folder that stands in for it
	// on a developer machine (the localstorage binding's root).
	Files []FilePort `json:"files,omitempty"`
}

// FilePort is one port's development file resolution. RootPath is declared
// relative to the system host's directory ("./test/erp-source"); the host
// library rejects paths that escape it, and consumers confine again before
// writing.
type FilePort struct {
	Port     string `json:"port"`
	RootPath string `json:"rootPath"`
}

// Component is a deployable block of the system. Kind is the block type
// (extractor, loader, transactional-integration, …). A component's directory,
// which joins it to its .intropy/scaffold.json project, is its Name relative
// to the system root.
type Component struct {
	Name       string        `json:"name"`
	Kind       string        `json:"kind"`
	Subscribes []TopicRef    `json:"subscribes,omitempty"`
	Publishes  []Publication `json:"publishes,omitempty"`
	Ports      []PortUse     `json:"ports,omitempty"`
	// InternalQueue is a transactional integration's own hop between its
	// receive and send sides, and the Subscription its send side receives it
	// through.
	InternalQueue *InternalQueue `json:"internalQueue,omitempty"`
	// Provides/Consumes are contract surfaces, parsed opaquely (see APIs).
	Provides []json.RawMessage `json:"provides,omitempty"`
	Consumes []json.RawMessage `json:"consumes,omitempty"`
}

// TopicRef is a component's subscription to a pub/sub topic. Routes are its
// rules in the order the sidecar evaluates them; Default says what happens to
// an event no route matches: DefaultIgnore acknowledges it, DefaultDeadLetter
// leaves it for the broker to dead-letter. A record without Routes predates
// routing: the subscription takes every message on the topic.
//
// Resource, DefaultPath and Bulk describe the declarative Dapr Subscription
// the host renders for it, alongside each route's Match and Path — exactly as
// a local run loads it, so a deployment renders the same resource without
// re-deriving the conventions. Records older than that leave them empty.
type TopicRef struct {
	PubSub      string  `json:"pubsub"`
	Topic       string  `json:"topic"`
	Routes      []Route `json:"routes,omitempty"`
	Default     string  `json:"default,omitempty"`
	Resource    string  `json:"resource,omitempty"`
	DefaultPath string  `json:"defaultPath,omitempty"`
	Bulk        *Bulk   `json:"bulk,omitempty"`
}

// InternalQueue is a transactional integration's internal hop: the pub/sub
// and topic, and — on records new enough to carry it — the Subscription
// resource's name and default path. One kind of message travels it, so the
// Subscription has no rules.
type InternalQueue struct {
	PubSub      string `json:"pubsub"`
	Topic       string `json:"topic"`
	Resource    string `json:"resource,omitempty"`
	DefaultPath string `json:"defaultPath,omitempty"`
}

// Bulk is a bulk subscription's batching, in the Subscription resource's units.
type Bulk struct {
	MaxMessagesCount   int   `json:"maxMessagesCount"`
	MaxAwaitDurationMs int64 `json:"maxAwaitDurationMs"`
}

// The values of TopicRef.Default.
const (
	DefaultIgnore     = "ignore"
	DefaultDeadLetter = "dead-letter"
)

// Route is one rule of a subscription: the message it takes (its name is its
// CloudEvent type) and, when set, the content filter (a CEL expression) its
// events must also match. Match and Path are the rule as the Subscription
// resource renders it.
type Route struct {
	Message string `json:"message"`
	When    string `json:"when,omitempty"`
	Match   string `json:"match,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Publication is a component's output onto a pub/sub topic. Message names
// what it publishes there; records older than message wiring omit it.
type Publication struct {
	PubSub  string `json:"pubsub"`
	Topic   string `json:"topic"`
	Message string `json:"message,omitempty"`
}

// PortUse is a component's use of an external port. Direction is "in"
// (external → component) or "out" (component → external).
type PortUse struct {
	Port      string `json:"port"`
	Direction string `json:"direction"`
}

// Topic is a declared pub/sub topic: the metadata for a (PubSub, Topic) pair
// components reference. Messages names the messages it carries, each with its
// own contract in the messagegroups section; Publishers/Subscribers name the
// components on each end. Contract is set only by records older than message
// wiring, which gave a topic a single contract: read contracts through
// Topology.TopicContracts, which covers both.
type Topic struct {
	PubSub      string   `json:"pubsub"`
	Topic       string   `json:"topic"`
	Messages    []string `json:"messages,omitempty"`
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

// Port is an external system integration point. The name is its whole
// identity — the deployed Dapr binding type, address, and credentials are
// environment-owned deployment configuration, never part of the record.
// ExternalSystem is the system it fronts, Directions the directions it is
// wired in, and UsedBy the components that use it.
type Port struct {
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
