package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// ManifestInspection is the environment-neutral deployment model derived from
// a system topology. Field names are stable and additive-only.
type ManifestInspection struct {
	System        string              `json:"system"`
	Template      string              `json:"template"`
	LocalFixtures []string            `json:"localFixtures,omitempty"`
	Components    []ManifestComponent `json:"components"`
	PubSubs       []ManifestPubSub    `json:"pubsubs"`
	Topics        []ManifestTopic     `json:"topics"`
	Ports         []InspectedPort     `json:"ports"`
}

// InspectedPort is one external edge derived from the topology.
type InspectedPort struct {
	Name           string   `json:"name"`
	ExternalSystem string   `json:"externalSystem,omitempty"`
	Directions     []string `json:"directions,omitempty"`
	AppIDs         []string `json:"appIds,omitempty"`
}

// InspectManifestOptions configures InspectManifests.
type InspectManifestOptions struct {
	System          string
	SourceDir       string
	TopologyFile    string
	TemplateVersion string
	OutputFormat    string
	UserAgent       string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Owner           string
	Repo            string
	GitHubBaseURL   string
	HTTP            *http.Client
}

// RenderManifestOptions configures RenderManifests.
type RenderManifestOptions struct {
	Environment     string
	System          string
	SourceDir       string
	TopologyFile    string
	TemplateVersion string
	Namespace       string
	Images          []string
	Bindings        []string
	Selector        interactive.Selector
	Runner          command.Runner
	UserAgent       string
	Stdin           io.Reader
	Stderr          io.Writer
	Owner           string
	Repo            string
	GitHubBaseURL   string
	HTTP            *http.Client
}

// CreateManifestOptions configures CreateManifests.
type CreateManifestOptions struct {
	Environment     string
	Bindings        []string
	Selector        interactive.Selector
	Domain          string
	System          string
	SourceDir       string
	TopologyFile    string
	TemplateVersion string
	GitopsRepo      string
	DryRun          bool
	Diff            bool
	CacheRoot       string
	Runner          command.Runner
	UserAgent       string
	CliVersion      string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Owner           string
	Repo            string
	GitHubBaseURL   string
	HTTP            *http.Client
}

// InspectManifests reports the topology and recorded deployment choices without
// rendering manifests or changing project and Git state.
func InspectManifests(ctx context.Context, opts InspectManifestOptions) error {
	initOpts := manifestRunOptions{
		Mode:            modeLocal,
		System:          opts.System,
		SourceDir:       opts.SourceDir,
		TopologyFile:    opts.TopologyFile,
		TemplateVersion: opts.TemplateVersion,
		OutputFormat:    opts.OutputFormat,
		UserAgent:       opts.UserAgent,
		Stdin:           opts.Stdin,
		Stdout:          opts.Stdout,
		Stderr:          opts.Stderr,
		Owner:           opts.Owner,
		Repo:            opts.Repo,
		GitHubBaseURL:   opts.GitHubBaseURL,
		HTTP:            opts.HTTP,
	}
	initOpts.applyDefaults()

	found, lib, err := prepareManifests(ctx, initOpts)
	if err != nil {
		return err
	}
	defer lib.Close()

	facts, err := resolveLocalFacts(initOpts, found)
	if err != nil {
		return err
	}
	fixtures, err := fixtureCatalog(lib)
	if err != nil {
		return err
	}

	result := ManifestInspection{
		System:        facts.System,
		Template:      lib.Ref(),
		LocalFixtures: fixtures,
		Components:    facts.Model.Components,
		PubSubs:       facts.Model.PubSubs,
		Topics:        facts.Model.Topics,
	}
	for _, conn := range facts.Model.Ports {
		result.Ports = append(result.Ports, InspectedPort{
			Name:           conn.Name,
			ExternalSystem: conn.ExternalSystem,
			Directions:     conn.Directions,
			AppIDs:         conn.AppIDs,
		})
	}

	if initOpts.OutputFormat == OutputJSON {
		enc := json.NewEncoder(initOpts.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("write JSON result: %w", err)
		}
		return nil
	}
	return reportManifestInspection(initOpts.Stdout, result)
}

// RenderManifests builds one environment and returns its complete YAML stream.
// Returning bytes keeps stdout unreachable until every render and validation succeeds.
func RenderManifests(ctx context.Context, opts RenderManifestOptions) ([]byte, error) {
	if opts.Environment != localEnv {
		return nil, fmt.Errorf("manifest rendering supports environment %q only", localEnv)
	}
	initOpts := manifestRunOptions{
		Mode:            modeLocal,
		System:          opts.System,
		SourceDir:       opts.SourceDir,
		TopologyFile:    opts.TopologyFile,
		TemplateVersion: opts.TemplateVersion,
		Namespace:       opts.Namespace,
		Images:          opts.Images,
		Bindings:        opts.Bindings,
		Selector:        opts.Selector,
		Runner:          opts.Runner,
		UserAgent:       opts.UserAgent,
		Stdin:           opts.Stdin,
		Stderr:          opts.Stderr,
		Owner:           opts.Owner,
		Repo:            opts.Repo,
		GitHubBaseURL:   opts.GitHubBaseURL,
		HTTP:            opts.HTTP,
	}
	initOpts.applyDefaults()

	found, lib, err := prepareManifests(ctx, initOpts)
	if err != nil {
		return nil, err
	}
	defer lib.Close()
	return renderLocalManifests(ctx, initOpts, found, lib)
}

// CreateManifests creates missing GitOps manifest files on a review branch.
// Existing identical files are accepted; existing differing files are never replaced.
func CreateManifests(ctx context.Context, opts CreateManifestOptions) error {
	environments := []string(nil)
	reviewEnv := "all"
	if opts.Environment != "" {
		environments = []string{opts.Environment}
		reviewEnv = opts.Environment
	}
	initOpts := manifestRunOptions{
		Mode:            modeGitOps,
		Domain:          opts.Domain,
		System:          opts.System,
		SourceDir:       opts.SourceDir,
		TopologyFile:    opts.TopologyFile,
		Environments:    environments,
		Bindings:        opts.Bindings,
		Selector:        opts.Selector,
		TemplateVersion: opts.TemplateVersion,
		GitopsRepo:      opts.GitopsRepo,
		PlanOnly:        opts.DryRun || opts.Diff,
		CacheRoot:       opts.CacheRoot,
		Runner:          opts.Runner,
		UserAgent:       opts.UserAgent,
		CliVersion:      opts.CliVersion,
		Stdin:           opts.Stdin,
		Stdout:          opts.Stdout,
		Stderr:          opts.Stderr,
		Owner:           opts.Owner,
		Repo:            opts.Repo,
		GitHubBaseURL:   opts.GitHubBaseURL,
		HTTP:            opts.HTTP,
		diffOnly:        opts.Diff,
		reviewEnv:       reviewEnv,
	}
	initOpts.applyDefaults()

	found, lib, err := prepareManifests(ctx, initOpts)
	if err != nil {
		return err
	}
	defer lib.Close()
	return initGitOps(ctx, initOpts, found, lib)
}

func prepareManifests(ctx context.Context, opts manifestRunOptions) (discoveredTopology, *template.Library, error) {
	found, err := discoverManifestTopology(ctx, opts)
	if err != nil {
		return discoveredTopology{}, nil, err
	}
	lib, err := template.FetchLibrary(ctx, template.LibraryOptions{
		Version:       opts.TemplateVersion,
		HTTP:          opts.HTTP,
		UserAgent:     opts.UserAgent,
		Stderr:        opts.Stderr,
		Owner:         opts.Owner,
		Repo:          opts.Repo,
		GitHubBaseURL: opts.GitHubBaseURL,
	})
	if err != nil {
		return discoveredTopology{}, nil, err
	}
	return found, lib, nil
}

func reportManifestInspection(w io.Writer, result ManifestInspection) error {
	if w == nil {
		w = os.Stdout
	}
	fixtures := strings.Join(result.LocalFixtures, ", ")
	if fixtures == "" {
		fixtures = "none"
	}
	fmt.Fprintf(w, "system          %s\ntemplate        %s\nlocal fixtures  %s\n",
		result.System, result.Template, fixtures)

	fmt.Fprintln(w, "\ncomponents")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, component := range result.Components {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", component.Name, component.Kind, component.Workload)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w, "\nports")
	if len(result.Ports) == 0 {
		fmt.Fprintln(w, "  none")
		return nil
	}
	for _, port := range result.Ports {
		details := port.ExternalSystem
		if details == "" {
			details = "external system not declared"
		}
		fmt.Fprintf(w, "  %s  %s\n", port.Name, details)
	}
	return nil
}
