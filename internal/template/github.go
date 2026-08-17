package template

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const (
	defaultTemplateOwner = "integrio-intropy"
	defaultTemplateRepo  = "intropy-templates"
	templateManifestName = "template.yaml"
	templateSkeletonDir  = "skeleton"
	githubAPIBaseURL     = "https://api.github.com"
)

// GitHub talks to a GitHub-compatible API. BaseURL is overridable for tests.
type GitHub struct {
	BaseURL   string
	HTTP      *http.Client
	Token     string
	UserAgent string
}

func newGitHub(client *http.Client, userAgent string) *GitHub {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHub{
		BaseURL:   githubAPIBaseURL,
		HTTP:      client,
		Token:     os.Getenv("GITHUB_TOKEN"),
		UserAgent: userAgent,
	}
}

// NewGitHubClient creates a configured GitHub client for external callers.
func NewGitHubClient(client *http.Client, userAgent, baseURL string) *GitHub {
	gh := newGitHub(client, userAgent)
	if baseURL != "" {
		gh.BaseURL = baseURL
	}
	return gh
}

func newConfiguredGitHub(client *http.Client, userAgent, baseURL string) *GitHub {
	return NewGitHubClient(client, userAgent, baseURL)
}

// ResolveTag returns requestedTag as-is when set, otherwise the latest
// release tag for owner/repo.
func (g *GitHub) ResolveTag(ctx context.Context, owner, repo, requestedTag string) (string, error) {
	if requestedTag != "" {
		return requestedTag, nil
	}
	return g.LatestTag(ctx, owner, repo)
}

// LatestTag returns the tag_name of the most recent release for owner/repo.
func (g *GitHub) LatestTag(ctx context.Context, owner, repo string) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", g.BaseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	g.addCommonHeaders(req)

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github releases: %s: %s", resp.Status, string(body))
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("github releases: empty tag_name")
	}
	return rel.TagName, nil
}

func (g *GitHub) addCommonHeaders(req *http.Request) {
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
}
