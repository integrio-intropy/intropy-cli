package oci

import "context"

func (c *Client) Resolve(ctx context.Context, ref string) (Descriptor, error) {
	return c.reg.Resolve(ctx, ref)
}
