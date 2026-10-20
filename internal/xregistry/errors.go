package xregistry

import "fmt"

// The typed lookup failures. Each names the entity it failed on so the
// command layer can map them to the CLI error voice — front-loaded
// failure, remediation on the second line — without re-parsing strings.

// MessageNotFoundError reports a reference that matched no message.
type MessageNotFoundError struct{ Ref string }

func (e *MessageNotFoundError) Error() string {
	return fmt.Sprintf("message %q not found in the registry", e.Ref)
}

// SchemaNotFoundError reports a message's dataschemaxid that resolves to no
// schema default version in the export.
type SchemaNotFoundError struct{ XID string }

func (e *SchemaNotFoundError) Error() string {
	return fmt.Sprintf("schema %q not found in the registry", e.XID)
}
