package template

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// PreparedCreate is a create run with everything resolved and nothing written:
// the manifest, the resolved values, the skeleton root, the release it came
// from, and the cleanup that removes the extracted library. RunCreate turns it
// into files on disk.
//
// It exists for callers that fetch the library themselves — the dashboard
// resolves one release for a whole form session and renders against that,
// where Create would re-resolve (and re-download) per request. The caller owns
// Cleanup and must call it once the run is done.
type PreparedCreate struct {
	// Template is the repo directory name of the requested template — what a
	// later re-fetch needs, as on CreateResult.
	Template string

	// Manifest is the template's parsed manifest.
	Manifest *Template

	// Values are the fully resolved render values.
	Values map[string]any

	// SkeletonRoot is the extracted skeleton/ directory Render reads.
	SkeletonRoot string

	// RepoRoot is the extracted library root, so dependency renders find the
	// sibling templates of the same release.
	RepoRoot string

	// Owner, Repo and Version describe the library the template came from.
	Owner   string
	Repo    string
	Version string

	// Cleanup removes the extracted library.
	Cleanup func()
}

// PrepareCreate fetches the named template at the requested version (or
// latest) and resolves its values, stopping before any output is written —
// the first half of Create. Files and SetValues feed Resolve exactly as
// Create's --values and --set do; a nil prompter makes a missing required
// value the clean "missing required parameter(s)" error rather than a prompt.
func PrepareCreate(ctx context.Context, opts CreateOptions) (*PreparedCreate, error) {
	opts.applyDefaults()
	if err := validateTemplateName(opts.Template); err != nil {
		return nil, err
	}

	gh := newConfiguredGitHub(opts.HTTP, opts.UserAgent, opts.GitHubBaseURL)
	tag, err := resolveReleaseTag(ctx, gh, opts.Owner, opts.Repo, opts.Version)
	if err != nil {
		return nil, err
	}

	templateRoot, cleanup, err := downloadTemplate(ctx, gh, opts.Owner, opts.Repo, tag, opts.Template, "intropy-template-*")
	if err != nil {
		return nil, err
	}

	tmpl, values, err := prepareCreateTemplate(templateRoot, opts)
	if err != nil {
		cleanup()
		return nil, err
	}

	skelRoot := filepath.Join(templateRoot, templateSkeletonDir)
	if info, err := os.Stat(skelRoot); err != nil || !info.IsDir() {
		cleanup()
		return nil, fmt.Errorf("template %q is missing %s/ directory", opts.Template, templateSkeletonDir)
	}

	return &PreparedCreate{
		Template:     opts.Template,
		Manifest:     tmpl,
		Values:       values,
		SkeletonRoot: skelRoot,
		RepoRoot:     filepath.Dir(templateRoot),
		Owner:        opts.Owner,
		Repo:         opts.Repo,
		Version:      tag,
		Cleanup:      cleanup,
	}, nil
}

// RunCreate executes the second half of Create: render the skeleton into
// outputDir, process declared dependencies, record the scaffold, and return
// the machine-readable result. The prompter question is already settled —
// PrepareCreate resolved the values — so RunCreate never interacts.
func RunCreate(p *PreparedCreate, outputDir string, force bool, stderr io.Writer) (*CreateResult, error) {
	if outputDir == "" {
		return nil, errors.New("--out-dir is required")
	}
	if stderr == nil {
		stderr = io.Discard
	}

	if err := renderCreateOutput(p.SkeletonRoot, p.Manifest, p.Template, outputDir, force, p.Values); err != nil {
		return nil, err
	}
	fmt.Fprintf(stderr, "created %s from %s/%s@%s (template %s)\n", outputDir, p.Owner, p.Repo, p.Version, p.Template)

	// Dependencies come from the same extracted tarball, so they are always
	// version-locked to the component that declared them.
	depRecords, depResults, err := processDependencies(p.Manifest, p.Values, outputDir, &depContext{
		repoRoot: p.RepoRoot,
		owner:    p.Owner,
		repo:     p.Repo,
		version:  p.Version,
		stderr:   stderr,
		visited:  map[string]bool{},
	}, 0)
	if err != nil {
		return nil, err
	}

	// The template field is the repo directory name (p.Template), not
	// p.Manifest.Metadata.Name — it is what a later re-fetch needs.
	if err := WriteScaffold(outputDir, Scaffold{
		SchemaVersion: ScaffoldSchemaVersion,
		Template:      p.Template,
		Owner:         p.Owner,
		Repo:          p.Repo,
		Version:       p.Version,
		Values:        p.Values,
		Role:          roleFromLabels(p.Manifest.Metadata.Labels),
		BlockKind:     blockKindFromLabels(p.Manifest.Metadata.Labels),
		DataFlow:      dataFlowFromLabels(p.Manifest.Metadata.Labels),
		DependsOn:     depRecords,
	}); err != nil {
		return nil, err
	}

	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		absOut = outputDir
	}
	return &CreateResult{
		Template:     p.Manifest.Metadata.Name,
		Owner:        p.Owner,
		Repo:         p.Repo,
		Version:      p.Version,
		OutputDir:    absOut,
		Values:       p.Values,
		Dependencies: depResults,
	}, nil
}
