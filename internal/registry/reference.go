package registry

import (
	"fmt"
	"strings"
)

// Reference is a parsed OCI ref: host, repository path, and the optional
// tag and digest that pin it. Either or both of Tag and Digest may be
// empty; emptiness is a caller concern, not a parse failure.
type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

// ParseReference splits host/repository[:tag][@digest]. The split is
// deliberately minimal — no normalisation, no default tag — so what the
// caller parsed is what the caller typed.
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

// TagOrDigest returns the tag when present, otherwise the digest. When a
// reference carries both, the tag wins — matching what this client has
// always done on pull.
func (r Reference) TagOrDigest() string {
	if r.Tag != "" {
		return r.Tag
	}
	return r.Digest
}

// String reassembles the canonical host/repository[:tag][@digest] form.
func (r Reference) String() string {
	s := r.Registry + "/" + r.Repository
	if r.Tag != "" {
		s += ":" + r.Tag
	}
	if r.Digest != "" {
		s += "@" + r.Digest
	}
	return s
}
