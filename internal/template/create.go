package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type CreateOptions struct {
	Template   string // required; subdirectory in the templates repo holding template.yaml + skeleton/
	OutputDir  string
	Version    string
	SetValues  map[string]any
	Files      []string
	Force      bool
	NoInput    bool
	OutputJSON string // path to write CreateResult JSON; "-" means stdout
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	HTTP       *http.Client
	UserAgent  string

	// Test overrides. Production callers leave these zero-valued; the CLI
	// always targets the official template library at integrio-intropy/intropy-templates.
	Owner         string
	Repo          string
	GitHubBaseURL string
}

// CreateResult is the machine-readable summary written when --output-json is
// set. It is the contract chained scaffolder steps consume; field names are
// stable and additive-only.
type CreateResult struct {
	Template  string         `json:"template"`
	Owner     string         `json:"owner"`
	Repo      string         `json:"repo"`
	Version   string         `json:"version"`
	OutputDir string         `json:"outputDir"`
	Values    map[string]any `json:"values"`
}

func (o *CreateOptions) applyDefaults() {
	if o.Owner == "" {
		o.Owner = defaultTemplateOwner
	}
	if o.Repo == "" {
		o.Repo = defaultTemplateRepo
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// Create runs the full scaffold: resolve release, download tarball, extract,
// load manifest, resolve values (with optional interactive prompting), render.
func Create(ctx context.Context, opts CreateOptions) error {
	opts.applyDefaults()
	if err := validateCreateOptions(opts); err != nil {
		return err
	}

	gh := newConfiguredGitHub(opts.HTTP, opts.UserAgent, opts.GitHubBaseURL)
	tag, err := resolveReleaseTag(ctx, gh, opts.Owner, opts.Repo, opts.Version)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "fetching %s/%s@%s\n", opts.Owner, opts.Repo, tag)

	templateRoot, cleanup, err := downloadTemplate(ctx, gh, opts.Owner, opts.Repo, tag, opts.Template, "intropy-template-*")
	if err != nil {
		return err
	}
	defer cleanup()

	tmpl, values, err := prepareCreateTemplate(templateRoot, opts)
	if err != nil {
		return err
	}

	if err := renderCreateOutput(templateRoot, opts.Template, opts.OutputDir, opts.Force, values); err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "created %s from %s/%s@%s (template %s)\n", opts.OutputDir, opts.Owner, opts.Repo, tag, opts.Template)

	// The template field is the repo directory name (opts.Template), not
	// tmpl.Metadata.Name — it is what a later re-fetch needs.
	if err := WriteScaffold(opts.OutputDir, Scaffold{
		SchemaVersion: ScaffoldSchemaVersion,
		Template:      opts.Template,
		Owner:         opts.Owner,
		Repo:          opts.Repo,
		Version:       tag,
		Values:        values,
	}); err != nil {
		return err
	}

	return maybeWriteCreateResult(opts, tmpl, values, tag)
}

func validateCreateOptions(opts CreateOptions) error {
	if err := validateTemplateName(opts.Template); err != nil {
		return err
	}
	if opts.OutputDir == "" {
		return errors.New("--output is required")
	}
	return nil
}

func prepareCreateTemplate(templateRoot string, opts CreateOptions) (*Template, map[string]any, error) {
	tmpl, err := LoadTemplate(filepath.Join(templateRoot, templateManifestName))
	if err != nil {
		return nil, nil, err
	}

	prompter := selectPrompter(&opts)
	values, err := Resolve(tmpl, opts.Files, opts.Stdin, opts.SetValues, prompter)
	if err != nil {
		return nil, nil, err
	}
	return tmpl, values, nil
}

func renderCreateOutput(templateRoot, templateName, outputDir string, force bool, values map[string]any) error {
	skelRoot := filepath.Join(templateRoot, templateSkeletonDir)
	if info, err := os.Stat(skelRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("template %q is missing %s/ directory", templateName, templateSkeletonDir)
	}
	if err := ensureOutputDir(outputDir, force); err != nil {
		return err
	}
	return Render(skelRoot, outputDir, values)
}

func maybeWriteCreateResult(opts CreateOptions, tmpl *Template, values map[string]any, tag string) error {
	if opts.OutputJSON == "" {
		return nil
	}

	absOut, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		absOut = opts.OutputDir
	}
	result := CreateResult{
		Template:  tmpl.Metadata.Name,
		Owner:     opts.Owner,
		Repo:      opts.Repo,
		Version:   tag,
		OutputDir: absOut,
		Values:    values,
	}
	if err := writeOutputJSON(opts.OutputJSON, opts.Stdout, result); err != nil {
		return fmt.Errorf("write --output-json: %w", err)
	}
	return nil
}

func writeOutputJSON(path string, stdout io.Writer, r CreateResult) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// EnsureOutputDir creates dir if missing and refuses to render into a
// non-empty directory unless force is set.
func EnsureOutputDir(dir string, force bool) error {
	return ensureOutputDir(dir, force)
}

func ensureOutputDir(dir string, force bool) error {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("--output %s exists and is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("--output %s is not empty (use --force to overwrite)", dir)
	}
	return nil
}

// validateTemplateName rejects empty names and anything that could escape the
// extracted tarball root via filepath.Join (separators, parent refs, hidden
// directories). The template argument is user input that we turn directly into
// a path segment, so it has to be sanitized.
func validateTemplateName(name string) error {
	if name == "" {
		return errors.New("template name is required")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid template name %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid template name %q (must be a single path segment)", name)
	}
	return nil
}

func selectPrompter(opts *CreateOptions) Prompter {
	return AutoPrompter(opts.Stdin, opts.Stderr, opts.NoInput)
}

// AutoPrompter returns a StdinPrompter when interactive prompting is viable:
// noInput is false and stdin is a real terminal. In CI / piped contexts it
// returns nil, so Resolve reports a clean "missing required parameter(s)"
// error instead of hanging on a read.
func AutoPrompter(stdin io.Reader, out io.Writer, noInput bool) Prompter {
	if noInput {
		return nil
	}
	f, ok := stdin.(*os.File)
	if !ok || !isTerminal(f.Fd()) {
		return nil
	}
	return NewStdinPrompter(stdin, out)
}
