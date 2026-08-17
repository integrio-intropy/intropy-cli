package template

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// ListOptions selects which template library release to list. Fields mirror
// DescribeOptions so callers can share configuration.
type ListOptions struct {
	Version string

	HTTP      *http.Client
	UserAgent string

	// Owner and Repo select the template library; zero values target the
	// official library. Source carries the fetch seams (GitHubBaseURL
	// redirects the latest-release API call in tests).
	Owner  string
	Repo   string
	Source SourceOptions
}

func (o *ListOptions) applyDefaults() {
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
// and Create. The listing reads the cached checkout rather than the GitHub
// contents API, so it answers offline whenever the cache can.
func List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	opts.applyDefaults()

	s := opts.Source
	s.Version, s.UserAgent = opts.Version, opts.UserAgent
	if s.Owner == "" && s.Repo == "" {
		s.Owner, s.Repo = libraryIdentity(opts.Owner, opts.Repo)
	}
	src, err := FetchSource(ctx, s)
	if err != nil {
		return nil, err
	}
	tag := src.Version
	names, err := listTemplateDirs(src.root)
	if err != nil {
		return nil, fmt.Errorf("list templates in %s/%s@%s: %w", opts.Owner, opts.Repo, tag, err)
	}
	return &ListResult{
		Owner:     opts.Owner,
		Repo:      opts.Repo,
		Version:   tag,
		Templates: names,
	}, nil
}

// listTemplateDirs returns the sorted non-hidden directory names under root —
// the template names of one library checkout.
func listTemplateDirs(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, ent := range ents {
		if ent.IsDir() && !strings.HasPrefix(ent.Name(), ".") {
			names = append(names, ent.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
