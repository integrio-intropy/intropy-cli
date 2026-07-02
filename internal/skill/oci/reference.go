package oci

import (
	"fmt"
	"strings"
)

type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

func ParseReference(s string) (Reference, error) {
	// Split on '@' first to peel off the digest, if present.
	base, digest := s, ""
	if i := strings.LastIndex(s, "@"); i >= 0 {
		base, digest = s[:i], s[i+1:]
	}

	before, after, ok := strings.Cut(base, "/")
	if !ok {
		return Reference{}, fmt.Errorf("invalid reference %q: no registry", s)
	}
	registry := before
	rest := after

	repo, tag := rest, ""
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		repo, tag = rest[:i], rest[i+1:]
	}

	return Reference{Registry: registry, Repository: repo, Tag: tag, Digest: digest}, nil
}
