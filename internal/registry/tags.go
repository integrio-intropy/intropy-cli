package registry

import (
	"context"
	"fmt"
)

// ListTags returns every tag in the repository named by ref. Any tag or digest
// on ref itself is ignored — only the repository part is used.
//
// A repository that does not exist yet returns ErrNotFound rather than an empty
// slice. Callers depend on telling the two apart: "this component has never
// been released" and "this component has been released, and something deleted
// every tag" are different situations, and only the first is routine.
//
// Order is whatever the registry reports. The distribution spec asks for
// lexical order but not every registry obliges, so callers that need a
// particular order must sort.
func (c *Client) ListTags(ctx context.Context, ref string) ([]string, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return nil, mapError(err, parsed)
	}

	return tags, nil
}
