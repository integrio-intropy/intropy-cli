package oci

import (
	"errors"
	"fmt"
	"regexp"
)

// 4.2 https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec#42-config-object-schema
type Config struct {
	SchemaVersion string         `json:"schemaVersion"`
	Name          string         `json:"name"`
	Version       string         `json:"version,omitempty"`
	Description   string         `json:"description,omitempty"`
	License       string         `json:"license,omitempty"`
	Compatibility string         `json:"compatibility,omitempty"`
	AllowedTools  []string       `json:"allowedTools,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// SupportedSchemaVersion is the only schemaVersion this implementation
// understands. Per §4.2, clients must reject unknown values rather than
// guessing at forward compatibility.
const SupportedSchemaVersion = "1"

// Limits from §4.2.
const (
	maxNameLength          = 64
	maxDescriptionLength   = 1024
	maxCompatibilityLength = 500
)

// nameRegex enforces the §4.2 name constraint:
// must start and end with [a-z0-9], may contain hyphens internally, total length 2–64.
var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// Validation errors. Sentinels so callers can branch with errors.Is.
var (
	ErrInvalidConfig        = errors.New("invalid skill config")
	ErrUnsupportedSchemaVer = fmt.Errorf("%w: unsupported schemaVersion", ErrInvalidConfig)
	ErrInvalidName          = fmt.Errorf("%w: invalid name", ErrInvalidConfig)
	ErrFieldTooLong         = fmt.Errorf("%w: field exceeds maximum length", ErrInvalidConfig)
)

func (c Config) Validate() error {
	if c.SchemaVersion == "" {
		return fmt.Errorf("%w: schemaVersion is required", ErrInvalidConfig)
	}
	if c.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("%w: %q (this client supports %q)", ErrUnsupportedSchemaVer, c.SchemaVersion, SupportedSchemaVersion)
	}

	if c.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidConfig)
	}
	if len(c.Name) > maxNameLength {
		return fmt.Errorf("%w: name length %d exceeds %d", ErrFieldTooLong, len(c.Name), maxNameLength)
	}
	if !nameRegex.MatchString(c.Name) {
		return fmt.Errorf("%w: %q does not match %s", ErrInvalidName, c.Name, nameRegex.String())
	}

	if len(c.Description) > maxDescriptionLength {
		return fmt.Errorf("%w: description length %d exceeds %d",
			ErrFieldTooLong, len(c.Description), maxDescriptionLength)
	}
	if len(c.Compatibility) > maxCompatibilityLength {
		return fmt.Errorf("%w: compatibility length %d exceeds %d",
			ErrFieldTooLong, len(c.Compatibility), maxCompatibilityLength)
	}

	return nil
}
