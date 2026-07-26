package oci

import (
	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// Client publishes and fetches agent skills. It applies the agent-skills
// artifact rules on top of the generic registry client.
type Client struct {
	reg *registry.Client
}

func NewClient(opts ...registry.Option) (*Client, error) {
	reg, err := registry.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{reg: reg}, nil
}
