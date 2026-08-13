// Package system assembles scaffolded integrations into a system host. It
// scans the workspace for the .intropy/scaffold.json records `int create`
// left behind, validates them into a system model, and builds the value
// payload the system-host template renders every declaration file from —
// Topics.cs, Connectors.cs, the development and system definitions, and
// the csproj. The template library owns all generated content; this
// package owns workspace knowledge only.
package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/huandu/xstrings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// hostTemplate is the directory in the templates repo holding the system
// host: the shell plus every declaration file, rendered from the payload.
const hostTemplate = "system-host"

type CreateOptions struct {
	Name       string // system name as typed; PascalCase or kebab-case
	StartDir   string // workspace scan root; default "."
	OutputDir  string // default: the kebab-cased name
	Version    string // template release tag; default: latest
	Force      bool
	OutputJSON string // path to write CreateResult JSON; "-" means stdout
	Stdout     io.Writer
	Stderr     io.Writer
	HTTP       *http.Client
	UserAgent  string

	// Owner and Repo select the template library; zero values target the
	// official library. GitHubBaseURL is a test-only seam.
	Owner         string
	Repo          string
	GitHubBaseURL string
}

// minFactsPayloadVersion is the lowest template-library release whose
// system-host consumes the facts-only payload. Named in the version gate's
// error so a user on an older release knows the floor.
const minFactsPayloadVersion = "v0.4.0"

// CreateResult is the machine-readable summary written when --output-json
// is set. Field names are stable and additive-only.
type CreateResult struct {
	Template  string         `json:"template"`
	Owner     string         `json:"owner"`
	Repo      string         `json:"repo"`
	Version   string         `json:"version"`
	OutputDir string         `json:"outputDir"`
	Values    map[string]any `json:"values"`
	System    Summary        `json:"system"`
}

// Summary describes the assembled system; it loosely mirrors the model the
// host's `graph` verb prints.
type Summary struct {
	Name          string      `json:"name"`
	Components    []Component `json:"components"`
	Topics        []Topic     `json:"topics"`
	Connectors    []Connector `json:"connectors,omitempty"`
	SharedLibrary string      `json:"sharedLibrary"` // scaffold directory
}

func (o *CreateOptions) applyDefaults() {
	if o.StartDir == "" {
		o.StartDir = "."
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// Create runs the full assembly: scan the workspace for scaffold records,
// validate them into a system model, build the value payload, and render
// the system-host template with it. All local validation happens before
// any network I/O.
func Create(ctx context.Context, opts CreateOptions) error {
	opts.applyDefaults()
	if opts.Name == "" {
		return fmt.Errorf("a system name is required")
	}

	// Exact parity with the templates' `{{ .name | kebabcase }}`: sprig
	// delegates to xstrings, so OrderFlow and order-flow are equivalent.
	kebab := xstrings.ToKebabCase(opts.Name)
	if opts.OutputDir == "" {
		opts.OutputDir = kebab
	}

	entries, warnings := template.ListScaffolds(opts.StartDir)
	for _, w := range warnings {
		fmt.Fprintf(opts.Stderr, "warning: %v\n", w)
	}
	warnf := func(format string, args ...any) {
		fmt.Fprintf(opts.Stderr, "warning: "+format+"\n", args...)
	}
	model, err := Assemble(entries, warnf)
	if err != nil {
		return err
	}
	model.Name = kebab

	payload, err := buildPayload(model, opts.OutputDir, kebab)
	if err != nil {
		return err
	}

	if err := template.Create(ctx, template.CreateOptions{
		Template:      hostTemplate,
		OutputDir:     opts.OutputDir,
		Version:       opts.Version,
		SetValues:     payload,
		Force:         opts.Force,
		NoInput:       true, // the template's values come from the payload, never prompts
		OnManifest:    requireFactsPayload,
		Stdin:         strings.NewReader(""),
		Stdout:        opts.Stdout,
		Stderr:        opts.Stderr,
		HTTP:          opts.HTTP,
		UserAgent:     opts.UserAgent,
		Owner:         opts.Owner,
		Repo:          opts.Repo,
		GitHubBaseURL: opts.GitHubBaseURL,
	}); err != nil {
		return err
	}

	// The template derives projectName/systemClass from the name; reading
	// them back from the record it just wrote keeps the result summary
	// honest about what was rendered.
	record, err := template.LoadScaffold(filepath.Join(opts.OutputDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		return err
	}
	hostEntry := template.ScaffoldEntry{Path: opts.OutputDir, Scaffold: *record}
	if model.ProjectName, err = stringValue(hostEntry, "projectName"); err != nil {
		return fmt.Errorf("template %s did not derive the host project name: %w", hostTemplate, err)
	}
	if model.SystemClass, err = stringValue(hostEntry, "systemClass"); err != nil {
		return fmt.Errorf("template %s did not derive the system class name: %w", hostTemplate, err)
	}

	contracts := "no contracts project"
	if model.Shared != nil {
		contracts = "contracts from " + model.Shared.Path
	}
	fmt.Fprintf(opts.Stderr, "assembled system %q: %d component(s), %d topic(s), %d connector(s), %s\n",
		model.Name, len(model.Components), len(model.Topics), len(model.Connectors), contracts)

	return maybeWriteCreateResult(opts, record, model)
}

// requireFactsPayload rejects a system-host template release that predates
// the facts-only payload: one that still requires sharedContracts consumes
// the pre-join payload shape (CLI-derived field identifiers and a csproj
// include path), which this CLI no longer produces. The gate runs before any
// output is written so an old release fails loudly instead of rendering a
// host that cannot compile.
func requireFactsPayload(t *template.Template) error {
	required, _ := t.Spec.Parameters["required"].([]any)
	for _, r := range required {
		if name, _ := r.(string); name == "sharedContracts" {
			return fmt.Errorf("system-host template %s requires the sharedContracts payload: it predates facts-only payloads (field identifiers and the csproj reference moved into the template) — use template release %s or newer", t.Metadata.Name, minFactsPayloadVersion)
		}
	}
	return nil
}

// sharedPath reports the detected contracts sibling for the JSON summary;
// empty when the workspace has none.
func sharedPath(m *Model) string {
	if m.Shared == nil {
		return ""
	}
	return m.Shared.Path
}

func maybeWriteCreateResult(opts CreateOptions, record *template.Scaffold, model *Model) error {
	if opts.OutputJSON == "" {
		return nil
	}
	absOut, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		absOut = opts.OutputDir
	}
	result := CreateResult{
		Template:  hostTemplate,
		Owner:     record.Owner,
		Repo:      record.Repo,
		Version:   record.Version,
		OutputDir: absOut,
		Values:    record.Values,
		System: Summary{
			Name:          model.Name,
			Components:    model.Components,
			Topics:        model.Topics,
			Connectors:    model.Connectors,
			SharedLibrary: sharedPath(model),
		},
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("write --output-json: %w", err)
	}
	data = append(data, '\n')
	if opts.OutputJSON == "-" {
		if _, err := opts.Stdout.Write(data); err != nil {
			return fmt.Errorf("write --output-json: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(opts.OutputJSON, data, 0o644); err != nil {
		return fmt.Errorf("write --output-json: %w", err)
	}
	return nil
}
