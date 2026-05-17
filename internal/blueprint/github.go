package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	defaultBlueprintOwner = "integrio-intropy"
	defaultBlueprintRepo  = "blueprints"
	templateManifestName  = "template.yaml"
	blueprintSkeletonDir  = "skeleton"
	githubAPIBaseURL      = "https://api.github.com"
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

func newConfiguredGitHub(client *http.Client, userAgent, baseURL string) *GitHub {
	gh := newGitHub(client, userAgent)
	if baseURL != "" {
		gh.BaseURL = baseURL
	}
	return gh
}

func resolveReleaseTag(ctx context.Context, gh *GitHub, owner, repo, requestedTag string) (string, error) {
	if requestedTag != "" {
		return requestedTag, nil
	}
	return gh.LatestTag(ctx, owner, repo)
}

func downloadBlueprint(ctx context.Context, gh *GitHub, owner, repo, tag, blueprint, tempPattern string) (string, func(), error) {
	rc, err := gh.Tarball(ctx, owner, repo, tag)
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()

	tmpDir, err := os.MkdirTemp("", tempPattern)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	if err := ExtractTarGz(rc, tmpDir); err != nil {
		cleanup()
		return "", nil, err
	}

	blueprintRoot := filepath.Join(tmpDir, blueprint)
	if info, err := os.Stat(blueprintRoot); err != nil || !info.IsDir() {
		cleanup()
		return "", nil, fmt.Errorf("blueprint %q not found in %s/%s@%s", blueprint, owner, repo, tag)
	}
	return blueprintRoot, cleanup, nil
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

// Tarball returns a streaming reader of the gzipped tar for owner/repo at tag.
// The caller must Close the returned ReadCloser.
func (g *GitHub) Tarball(ctx context.Context, owner, repo, tag string) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", g.BaseURL, owner, repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	g.addCommonHeaders(req)

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download tarball: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("download tarball: %s: %s", resp.Status, string(body))
	}
	return resp.Body, nil
}

func (g *GitHub) addCommonHeaders(req *http.Request) {
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
}
