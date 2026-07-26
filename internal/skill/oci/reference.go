package oci

import "github.com/integrio-intropy/intropy-cli/internal/registry"

type Reference = registry.Reference

func ParseReference(s string) (Reference, error) {
	return registry.ParseReference(s)
}
