// Package config reads the user-level intropy configuration file — settings
// that belong to a person and a machine rather than to a project, such as
// which GitOps repository to deploy through.
//
// Project-scoped state lives elsewhere and is found by walking up from the
// working directory (.intropy/scaffold.json); this package is
// only for the single per-user file.
//
// Precedence is flag > environment > active context > top-level keys. The
// top-level keys are defaults that the active context overrides; a file with
// no contexts behaves exactly as one written before contexts existed. The
// file is optional: a missing one yields the zero Config, because a user who
// passes every setting on the command line should never be told to create a
// config file. A file that exists but cannot be parsed is an error —
// silently ignoring it would mean a typo'd key looks exactly like a setting
// that was never set.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// EnvGitopsRepo overrides gitopsRepo. Settings this CLI owns are
	// namespaced with INTROPY_ so they cannot collide with other tools.
	EnvGitopsRepo = "INTROPY_GITOPS_REPO"

	// EnvArgocdServer and EnvArgocdAuthToken are deliberately *not*
	// namespaced: they are the argocd CLI's own variables, and reading them
	// means an existing Argo setup works here with no extra configuration.
	// Borrowed, not ours — do not rename them.
	EnvArgocdServer    = "ARGOCD_SERVER"
	EnvArgocdAuthToken = "ARGOCD_AUTH_TOKEN"

	// EnvTemplateRepo overrides templateRepo. The value is the template
	// library as owner/repo on GitHub.
	EnvTemplateRepo = "INTROPY_TEMPLATE_REPO"

	dirName  = "intropy"
	fileName = "config.yaml"
)

// Config is the on-disk file. Fields are optional; every one of them can also
// be supplied by flag or environment variable.
type Config struct {
	// Organization names the customer or organisation the settings belong to.
	// It has no consumer in this package; commands read the resolved value.
	Organization string `yaml:"organization"`

	// GitopsRepo is the clone URL of the GitOps repository that holds the
	// deployment overlays.
	GitopsRepo string `yaml:"gitopsRepo"`

	// ArgocdServer is a fallback for repositories whose deploy.yaml does not
	// name an ArgoCD server. deploy.yaml wins when both are present, since it
	// travels with the repository the overlays live in.
	ArgocdServer string `yaml:"argocdServer"`

	// TemplateRepo is the template library to scaffold from, as owner/repo on
	// GitHub. Empty targets the official library.
	TemplateRepo string `yaml:"templateRepo"`

	// CurrentContext selects which entry of Contexts is active. It must name
	// a key of Contexts; a dangling pointer is a load error, because
	// resolving against the wrong customer is worse than refusing to run.
	CurrentContext string `yaml:"currentContext"`

	// Contexts bundles the settings that change together when the user
	// changes customer. A context overrides only the top-level keys it sets;
	// the rest fall through to the file's defaults.
	Contexts map[string]Context `yaml:"contexts"`
}

// Context is one named bundle of customer-scoped settings. Every field is
// optional; an unset field falls through to the file's top-level default.
type Context struct {
	Organization string `yaml:"organization"`
	GitopsRepo   string `yaml:"gitopsRepo"`
	ArgocdServer string `yaml:"argocdServer"`
	TemplateRepo string `yaml:"templateRepo"`
}

// Flags carries the command-line values that take precedence over everything
// else. Empty fields fall through to the environment and then the file.
type Flags struct {
	Organization string
	GitopsRepo   string
	ArgocdServer string
	TemplateRepo string
}

// Dir returns the directory holding the configuration file. It honours
// XDG_CONFIG_HOME and otherwise uses ~/.config on every platform — matching
// argocd and gh rather than os.UserConfigDir, which on macOS would resolve to
// ~/Library/Application Support and put this file somewhere no one would look
// for it.
func Dir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, dirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", dirName), nil
}

// Path returns the full path of the configuration file, whether or not it
// exists. Use it in error messages so users know which file to create.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Load reads the configuration file. A missing file yields the zero Config
// and no error; anything else — unreadable, malformed, or carrying unknown
// keys — is an error naming the path.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return loadFile(path)
}

func loadFile(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	// Reject unknown keys: a misspelled setting that is silently dropped is
	// indistinguishable from one that was never set, and produces a confusing
	// failure much further along.
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file is a legitimate way to say "no settings".
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validateCurrentContext(path); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validateCurrentContext rejects a currentContext that names no entry of
// Contexts. The check runs at load so every command reports it, not just the
// ones that happen to read a context-backed setting.
func (c Config) validateCurrentContext(path string) error {
	if c.CurrentContext == "" {
		return nil
	}
	if _, ok := c.Contexts[c.CurrentContext]; ok {
		return nil
	}
	return unknownContextError(path, c.CurrentContext, c.Contexts)
}

// unknownContextError builds the message shared by load-time validation and
// 'context use': one builder so the two texts cannot drift. The format
// follows the house error voice — what failed on the first line, the valid
// values on the second.
func unknownContextError(path, name string, contexts map[string]Context) error {
	if len(contexts) == 0 {
		return fmt.Errorf("currentContext %q does not exist in %s\nno contexts configured in %s", name, path, path)
	}
	names := make([]string, 0, len(contexts))
	for n := range contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Errorf("currentContext %q does not exist in %s\nvalid contexts: %s", name, path, strings.Join(names, ", "))
}

// Resolve layers flag values over the environment, the active context, and
// then the file's top-level defaults. The result is what the command should
// actually use.
func (c Config) Resolve(flags Flags) Config {
	ctx := c.Contexts[c.CurrentContext]
	return Config{
		// Organization has no environment variable: it travels with the
		// customer, so it lives in contexts and flags only.
		Organization: cmp.Or(flags.Organization, ctx.Organization, c.Organization),
		GitopsRepo:   cmp.Or(flags.GitopsRepo, os.Getenv(EnvGitopsRepo), ctx.GitopsRepo, c.GitopsRepo),
		ArgocdServer: cmp.Or(flags.ArgocdServer, os.Getenv(EnvArgocdServer), ctx.ArgocdServer, c.ArgocdServer),
		TemplateRepo: cmp.Or(flags.TemplateRepo, os.Getenv(EnvTemplateRepo), ctx.TemplateRepo, c.TemplateRepo),
		// The resolved config answers "what am I pointed at"; the selection
		// itself is not re-layered, so both travel through unchanged.
		CurrentContext: c.CurrentContext,
		Contexts:       c.Contexts,
	}
}

// RequireGitopsRepo returns the resolved GitOps repository URL, or an error
// listing every way it can be supplied.
func (c Config) RequireGitopsRepo() (string, error) {
	if c.GitopsRepo != "" {
		return c.GitopsRepo, nil
	}
	path, err := Path()
	if err != nil {
		path = filepath.Join("~", ".config", dirName, fileName)
	}
	return "", fmt.Errorf("no GitOps repository configured; pass --gitops-repo, set %s, or add gitopsRepo to %s", EnvGitopsRepo, path)
}

// ParseTemplateRepo splits a templateRepo value into owner and repo. An empty
// value is valid and yields empty results, meaning the official library. The
// value must be exactly owner/repo: URLs and SSH remotes are rejected here
// rather than failing later as a confusing 404 from the GitHub API, and the
// library is fetched over the GitHub API, so nothing but GitHub can serve it.
func ParseTemplateRepo(s string) (owner, repo string, err error) {
	if s == "" {
		return "", "", nil
	}
	if strings.ContainsAny(s, ":@") || strings.Contains(s, "://") {
		return "", "", fmt.Errorf("template repository %q is not owner/repo — the template library is fetched from GitHub, so configure it as owner/repo (e.g. acme/intropy-templates)", s)
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("template repository %q is not owner/repo (e.g. acme/intropy-templates)", s)
	}
	return parts[0], parts[1], nil
}
