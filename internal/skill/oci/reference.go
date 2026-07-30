package oci

import "github.com/integrio-intropy/intropy-cli/internal/registry"

// Reference is an alias so skill code can name a registry reference without
// importing the generic package everywhere.
type Reference = registry.Reference

// ParseReference parses host/repository[:tag][@digest]. A missing tag is
// allowed here; callers that need one check parsed.Tag themselves.
func ParseReference(s string) (Reference, error) {
	return registry.ParseReference(s)
}
