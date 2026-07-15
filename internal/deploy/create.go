// Package deploy generates Kubernetes deployment manifests (a kustomize
// base + overlays tree) for a previously scaffolded integration. It reads
// the committed .intropy/scaffold.json record, re-fetches the exact
// template version it pins, and renders the template's manifests/
// directory with the blueprint package's template machinery.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/integrio-intropy/intropy-cli/internal/blueprint"
)

const (
	// manifestDirName is the directory inside a template (next to
	// skeleton/) that holds the deployment manifest template:
	// <template>/manifests/template.yaml + <template>/manifests/skeleton/.
	manifestDirName = "manifests"

	templateManifestName = "template.yaml"
	skeletonDirName      = "skeleton"

	// scaffoldValuesKey is the reserved key under which the full scaffold
	// value map is exposed to manifest templates ({{ .scaffold.name }}).
	scaffoldValuesKey = "scaffold"

	// defaultOutputDirName is the output directory relative to the project
	// root when --output is not given.
	defaultOutputDirName = "deploy"
)

type CreateOptions struct {
	StartDir   string // directory to begin the scaffold.json walk-up from; default "."
	OutputDir  string // default <projectRoot>/deploy
	Version    string // template release tag; default: the tag pinned in scaffold.json
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

	// Test overrides. Production callers leave these zero-valued; the
	// template is fetched from the owner/repo recorded in scaffold.json.
	Owner         string
	Repo          string
	GitHubBaseURL string
}

// CreateResult is the machine-readable summary written when --output-json is
// set. Field names are stable and additive-only.
type CreateResult struct {
	Template  string         `json:"template"`
	Owner     string         `json:"owner"`
	Repo      string         `json:"repo"`
	Version   string         `json:"version"`
	OutputDir string         `json:"outputDir"`
	Values    map[string]any `json:"values"`
}

func (o *CreateOptions) applyDefaults() {
	if o.StartDir == "" {
		o.StartDir = "."
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.UserAgent == "" {
		o.UserAgent = "intropy-cli"
	}
}

// Create generates the deployment manifests: locate scaffold.json, download
// the pinned template, load manifests/template.yaml, resolve values (seeded
// from the scaffold record, with optional interactive prompting), render
// manifests/skeleton into the output directory.
func Create(ctx context.Context, opts CreateOptions) error {
	opts.applyDefaults()

	scaffold, projectRoot, err := blueprint.FindScaffold(opts.StartDir)
	if err != nil {
		if errors.Is(err, blueprint.ErrScaffoldNotFound) {
			return fmt.Errorf("%w\nThis project was scaffolded before deployment metadata existed. Re-scaffold it with 'intropy int create', or commit a minimal %s:\n  {\"schemaVersion\":1,\"template\":\"<template>\",\"owner\":\"integrio-intropy\",\"repo\":\"intropy-templates\",\"version\":\"<tag>\",\"values\":{\"name\":\"<name>\"}}", err, blueprint.ScaffoldRelPath)
		}
		return err
	}
	if scaffold.SchemaVersion > blueprint.ScaffoldSchemaVersion {
		return fmt.Errorf("%s has schemaVersion %d, but this CLI supports up to %d; upgrade intropy", blueprint.ScaffoldRelPath, scaffold.SchemaVersion, blueprint.ScaffoldSchemaVersion)
	}
	owner, repo := scaffold.Owner, scaffold.Repo
	if opts.Owner != "" {
		owner = opts.Owner
	}
	if opts.Repo != "" {
		repo = opts.Repo
	}

	tag := scaffold.Version
	if opts.Version != "" {
		tag = opts.Version
	}
	if tag == "" {
		return fmt.Errorf("%s does not pin a template version; pass --version", blueprint.ScaffoldRelPath)
	}

	gh := blueprint.NewGitHubClient(opts.HTTP, opts.UserAgent, opts.GitHubBaseURL)
	fmt.Fprintf(opts.Stderr, "fetching %s/%s@%s\n", owner, repo, tag)
	blueprintRoot, cleanup, err := blueprint.DownloadBlueprint(ctx, gh, owner, repo, tag, scaffold.Template, "intropy-manifests-*")
	if err != nil {
		return err
	}
	defer cleanup()

	manifestsRoot := filepath.Join(blueprintRoot, manifestDirName)
	skelRoot := filepath.Join(manifestsRoot, skeletonDirName)
	if !dirExists(skelRoot) || !fileExists(filepath.Join(manifestsRoot, templateManifestName)) {
		return fmt.Errorf("template %q at %s does not include deployment manifest templates (%s/); pass --version <newer tag> or update the template", scaffold.Template, tag, manifestDirName)
	}

	tmpl, err := blueprint.LoadTemplate(filepath.Join(manifestsRoot, templateManifestName))
	if err != nil {
		return err
	}
	values, err := resolveManifestValues(tmpl, scaffold, opts)
	if err != nil {
		return err
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(projectRoot, defaultOutputDirName)
	}
	if err := blueprint.EnsureOutputDir(outputDir, opts.Force); err != nil {
		return err
	}
	if err := blueprint.Render(skelRoot, outputDir, values); err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "wrote deployment manifests to %s (template %s@%s)\n", outputDir, scaffold.Template, tag)

	return maybeWriteCreateResult(opts, scaffold, owner, repo, tag, outputDir, values)
}

// resolveManifestValues resolves the manifest template's parameters, seeded
// by the scaffold record: scaffold values whose names match declared
// parameters auto-fill (no re-prompt) but yield to --values/--set. The full
// scaffold value map is exposed under the reserved "scaffold" key — added
// after Resolve so it never contaminates JSON Schema validation.
func resolveManifestValues(tmpl *blueprint.Template, scaffold *blueprint.Scaffold, opts CreateOptions) (map[string]any, error) {
	declared := map[string]bool{}
	for _, f := range tmpl.Fields() {
		if f.Name == scaffoldValuesKey {
			return nil, fmt.Errorf("manifest template declares reserved parameter %q", scaffoldValuesKey)
		}
		declared[f.Name] = true
	}
	if _, ok := tmpl.Spec.Values[scaffoldValuesKey]; ok {
		return nil, fmt.Errorf("manifest template declares reserved spec.values entry %q", scaffoldValuesKey)
	}

	seed := map[string]any{}
	for k, v := range scaffold.Values {
		if declared[k] {
			seed[k] = v
		}
	}

	prompter := blueprint.AutoPrompter(opts.Stdin, opts.Stderr, opts.NoInput)
	values, err := blueprint.ResolveLayered(tmpl, seed, opts.Files, opts.Stdin, opts.SetValues, prompter)
	if err != nil {
		return nil, err
	}
	values[scaffoldValuesKey] = scaffold.Values
	return values, nil
}

func maybeWriteCreateResult(opts CreateOptions, scaffold *blueprint.Scaffold, owner, repo, tag, outputDir string, values map[string]any) error {
	if opts.OutputJSON == "" {
		return nil
	}
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		absOut = outputDir
	}
	result := CreateResult{
		Template:  scaffold.Template,
		Owner:     owner,
		Repo:      repo,
		Version:   tag,
		OutputDir: absOut,
		Values:    values,
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
