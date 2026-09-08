package template

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
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

// githubTimeout bounds a latest-release lookup. http.DefaultClient has none,
// so a connection that stalls rather than fails would hang the command — and
// in the dashboard it would hang every template request behind it.
const githubTimeout = 30 * time.Second

func newGitHub(client *http.Client, userAgent string) *GitHub {
	if client == nil {
		client = &http.Client{Timeout: githubTimeout}
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
		if limit := asRateLimit(resp, body); limit != nil {
			return "", fmt.Errorf("github releases: %w", limit)
		}
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

// RateLimitError reports a GitHub rate limit: the primary hourly budget, or
// the secondary limit that bursts of requests trip. It is a distinct type
// because the two answers a caller can give are different — a primary limit
// means authenticate or wait, a secondary one means slow down — and because
// a bare "403 Forbidden" reads as a permissions problem, which it is not.
type RateLimitError struct {
	// Secondary distinguishes the burst limit from the hourly budget.
	Secondary bool

	// RetryAfter is how long GitHub says to wait, zero when it says nothing.
	RetryAfter time.Duration

	Status string
}

func (e *RateLimitError) Error() string {
	kind := "github rate limit reached"
	if e.Secondary {
		kind = "github secondary rate limit reached"
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s; retry in %s", kind, e.RetryAfter.Round(time.Second))
	}
	return kind
}

// asRateLimit reports whether a non-OK response is a rate limit, and how long
// to wait. GitHub spends three shapes on this: 429, a 403 carrying an explicit
// Retry-After, and a 403 whose remaining budget is zero. Only the last is
// distinguishable from an ordinary permissions 403 by status alone, so the
// headers decide rather than the status code.
func asRateLimit(resp *http.Response, body []byte) *RateLimitError {
	retry := retryAfter(resp)
	exhausted := resp.Header.Get("x-ratelimit-remaining") == "0"
	secondary := strings.Contains(strings.ToLower(string(body)), "secondary rate limit")

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
	case resp.StatusCode == http.StatusForbidden && (exhausted || retry > 0 || secondary):
	default:
		return nil
	}
	// A named wait without an exhausted budget is the burst limit: the hourly
	// budget reports what is left, the secondary one only asks for a pause.
	return &RateLimitError{
		Secondary:  secondary || (retry > 0 && !exhausted),
		RetryAfter: retry,
		Status:     resp.Status,
	}
}

// retryAfter reads the wait GitHub named, preferring the explicit Retry-After
// over the reset timestamp of the hourly budget.
func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("retry-after"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if v := resp.Header.Get("x-ratelimit-reset"); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Until(time.Unix(unix, 0)); d > 0 {
				return d
			}
		}
	}
	return 0
}
