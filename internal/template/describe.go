package template

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
)

// DescribeOptions selects which template release to inspect. Fields mirror
// CreateOptions for the create flow so callers can share configuration.
type DescribeOptions struct {
	Template  string // required; subdirectory in the templates repo holding template.yaml
	Version   string
	HTTP      *http.Client
	UserAgent string

	// Owner and Repo select the template library; zero values target the
	// official library. Source carries the fetch seams (GitHubBaseURL
	// redirects the latest-release API call in tests).
	Owner  string
	Repo   string
	Source SourceOptions
}

func (o *DescribeOptions) applyDefaults() {
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// DescribeResult is the machine-readable view of a template manifest. The
// shape is stable and additive-only — Backstage's frontend renders the form
// from Parameters (a raw JSON Schema), so this contract is load-bearing.
type DescribeResult struct {
	Template    string            `json:"template"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Owner       string            `json:"owner"`
	Repo        string            `json:"repo"`
	Version     string            `json:"version"`
	Parameters  map[string]any    `json:"parameters"`

	// Dependencies lists the sibling templates this template scaffolds
	// alongside its own output (spec.dependencies).
	Dependencies []DependencySpec `json:"dependencies,omitempty"`

	// Fields is the parameter schema as FieldSpecs in YAML declaration
	// order — the order Parameters loses when the document round-trips
	// through a JSON map. Form renderers (the dashboard, editor extensions)
	// consume Fields and never touch the raw schema.
	Fields []FieldSpec `json:"fields"`
}

// Describe ensures the cached checkout at the requested version (or latest)
// and returns the template's parsed manifest. It performs the same fetch path
// as Create but stops short of value resolution or rendering.
func Describe(ctx context.Context, opts DescribeOptions) (*DescribeResult, error) {
	opts.applyDefaults()
	if err := validateTemplateName(opts.Template); err != nil {
		return nil, err
	}

	s := opts.Source
	s.Version, s.UserAgent = opts.Version, opts.UserAgent
	if s.Owner == "" && s.Repo == "" {
		s.Owner, s.Repo = libraryIdentity(opts.Owner, opts.Repo)
	}
	src, err := FetchSource(ctx, s)
	if err != nil {
		return nil, err
	}

	templateRoot, err := templateDir(src, src.Owner, src.Repo, opts.Template)
	if err != nil {
		return nil, err
	}

	tmpl, err := LoadTemplate(filepath.Join(templateRoot, templateManifestName))
	if err != nil {
		return nil, err
	}
	tag := src.Version

	return &DescribeResult{
		Template:     tmpl.Metadata.Name,
		Title:        tmpl.Metadata.Title,
		Description:  tmpl.Metadata.Description,
		Tags:         tmpl.Metadata.Tags,
		Labels:       tmpl.Metadata.Labels,
		Owner:        src.Owner,
		Repo:         src.Repo,
		Version:      tag,
		Parameters:   tmpl.Spec.Parameters,
		Dependencies: tmpl.Spec.Dependencies,
		Fields:       tmpl.Fields(),
	}, nil
}

// FormatText writes a human-readable summary of the template to w. The
// machine-readable form is JSON-marshaled DescribeResult — callers needing
// stable parsing should use that instead.
func (r *DescribeResult) FormatText(w io.Writer) {
	title := r.Template
	if r.Title != "" {
		title = fmt.Sprintf("%s (%s)", r.Title, r.Template)
	}
	fmt.Fprintf(w, "%s @ %s/%s@%s\n", title, r.Owner, r.Repo, r.Version)
	if r.Description != "" {
		fmt.Fprintf(w, "\n%s\n", r.Description)
	}
	if len(r.Tags) > 0 {
		fmt.Fprintf(w, "\nTags: %v\n", r.Tags)
	}
	if len(r.Dependencies) > 0 {
		fmt.Fprintln(w, "\nScaffolds alongside (skipped when already present):")
		for _, d := range r.Dependencies {
			fmt.Fprintf(w, "  %s -> ../%s\n", d.Template, d.Output)
		}
	}

	if len(r.Fields) > 0 {
		fmt.Fprintln(w, "\nParameters:")
		for _, f := range r.Fields {
			marker := " "
			if f.Required {
				marker = "*"
			}
			label := f.Name
			if f.Title != "" {
				label = fmt.Sprintf("%s — %s", f.Name, f.Title)
			}
			fmt.Fprintf(w, "  %s %s [%s]\n", marker, label, f.Type)
			if f.Description != "" {
				fmt.Fprintf(w, "      %s\n", f.Description)
			}
			if f.Default != nil {
				fmt.Fprintf(w, "      default: %v\n", f.Default)
			}
			if len(f.Enum) > 0 {
				fmt.Fprintf(w, "      values: %v\n", f.Enum)
			}
			if f.Pattern != "" {
				fmt.Fprintf(w, "      pattern: %s\n", f.Pattern)
			}
		}
		fmt.Fprintln(w, "  (* = required)")
	}
}
