package oci

import (
	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// Client publishes and fetches agent skills. It applies the agent-skills
// artifact rules on top of the generic registry client.
type Client struct {
	reg *registry.Client
}

// NewClient builds a Client on top of a generic registry client configured
// with opts. It exists to give the skills rules somewhere to live: the
// generic client knows OCI, this one knows what a skill artifact looks like.
func NewClient(opts ...registry.Option) (*Client, error) {
	reg, err := registry.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{reg: reg}, nil
}
