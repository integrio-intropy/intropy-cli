package template

import (
	"context"
	"fmt"
	"net/http"
	"sort"
)

// ListOptions selects which template library release to list. Fields mirror
// DescribeOptions so callers can share configuration.
type ListOptions struct {
	Version string

	HTTP      *http.Client
	UserAgent string

	// Owner and Repo select the template library; zero values target the
	// official library. GitHubBaseURL is a test-only seam.
	Owner         string
	Repo          string
	GitHubBaseURL string
}

func (o *ListOptions) applyDefaults() {
	if o.Owner == "" {
		o.Owner = defaultTemplateOwner
	}
	if o.Repo == "" {
		o.Repo = defaultTemplateRepo
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// ListResult is the machine-readable view of a template library listing.
type ListResult struct {
	Owner     string   `json:"owner"`
	Repo      string   `json:"repo"`
	Version   string   `json:"version"`
	Templates []string `json:"templates"`
}

// List returns the names of the templates in the library at the requested
// version (or latest). Names are the directory names accepted by Describe
// and Create.
func List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	opts.applyDefaults()

	gh := newConfiguredGitHub(opts.HTTP, opts.UserAgent, opts.GitHubBaseURL)
	tag, err := resolveReleaseTag(ctx, gh, opts.Owner, opts.Repo, opts.Version)
	if err != nil {
		return nil, err
	}
	names, err := gh.ListTemplates(ctx, opts.Owner, opts.Repo, tag)
	if err != nil {
		return nil, fmt.Errorf("list templates in %s/%s@%s: %w", opts.Owner, opts.Repo, tag, err)
	}
	sort.Strings(names)
	return &ListResult{
		Owner:     opts.Owner,
		Repo:      opts.Repo,
		Version:   tag,
		Templates: names,
	}, nil
}
