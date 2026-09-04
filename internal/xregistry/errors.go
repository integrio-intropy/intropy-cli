package xregistry

import (
	"fmt"
	"strings"
)

// The typed resolution failures. Each names the entity it failed on so the
// command layer can map them to the CLI error voice — front-loaded
// failure, remediation on the second line — without re-parsing strings.

// MessageNotFoundError reports a reference that matched no message.
type MessageNotFoundError struct{ Ref string }

func (e *MessageNotFoundError) Error() string {
	return fmt.Sprintf("message %q not found in the registry", e.Ref)
}

// NoProducerError reports a message group carried by no producer endpoint.
type NoProducerError struct{ Group string }

func (e *NoProducerError) Error() string {
	return fmt.Sprintf("message group %q has no producer endpoint in the registry", e.Group)
}

// AmbiguousProducerError reports several producer endpoints carrying the
// message's group on different channels. Candidates names both channels so
// an interactive run can offer the pick list a --no-input run refuses to
// choose from.
type AmbiguousProducerError struct {
	Ref      string
	Channels []Channel
}

func (e *AmbiguousProducerError) Error() string {
	var parts []string
	for _, c := range e.Channels {
		parts = append(parts, c.Pubsub+"/"+c.Topic)
	}
	return fmt.Sprintf("message %q is produced on %d channels: %s", e.Ref, len(e.Channels), strings.Join(parts, ", "))
}

// ChannelFormatError reports a channel the <pubsub>/<topic> split rejects;
// the channel format belongs to the registry's conformance suite, and a
// half-parsed channel must not become wiring.
type ChannelFormatError struct {
	Endpoint string
	Channel  string
	err      error
}

func (e *ChannelFormatError) Error() string {
	return fmt.Sprintf("endpoint %s: %v", e.Endpoint, e.err)
}

func (e *ChannelFormatError) Unwrap() error { return e.err }

// SchemaNotFoundError reports a message's dataschemaxid that resolves to no
// schema default version in the export.
type SchemaNotFoundError struct{ XID string }

func (e *SchemaNotFoundError) Error() string {
	return fmt.Sprintf("schema %q not found in the registry", e.XID)
}

// NoDefaultVersionError reports a message whose definition carries no
// attributes on any version — a registry document the client does not
// understand, not a wiring the CLI can guess.
type NoDefaultVersionError struct {
	Ref   string
	Group string
}

func (e *NoDefaultVersionError) Error() string {
	return fmt.Sprintf("message %q in group %q carries no definition attributes", e.Ref, e.Group)
}
