package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/spf13/cobra"
)

var contextShowOpts struct {
	output string
}

// contextSetting is one resolved setting with the layer it came from.
// Source is empty when the setting is unset.
type contextSetting struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
}

// contextShowJSON is the machine-readable document for 'context show': the
// resolved effective config with each value's source.
type contextShowJSON struct {
	Organization contextSetting `json:"organization"`
	GitopsRepo   contextSetting `json:"gitopsRepo"`
	TemplateRepo contextSetting `json:"templateRepo"`
	ArgocdServer contextSetting `json:"argocdServer"`
}

// resolveShowSettings loads and resolves the configuration the same way
// every command does — the flag rung is always empty here because show
// takes no value flags — and records which layer each value came from. The
// env rung matters: an exported variable the user forgot about is exactly
// what this command exists to expose.
func resolveShowSettings() (contextShowJSON, error) {
	cfg, err := config.Load()
	if err != nil {
		return contextShowJSON{}, err
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	sourced := func(envName, ctxVal, fileVal string) contextSetting {
		if v := os.Getenv(envName); envName != "" && v != "" {
			return contextSetting{Value: v, Source: "env"}
		}
		if ctxVal != "" {
			return contextSetting{Value: ctxVal, Source: "context"}
		}
		if fileVal != "" {
			return contextSetting{Value: fileVal, Source: "file"}
		}
		return contextSetting{}
	}
	return contextShowJSON{
		// Organization has no environment variable; see config.Resolve.
		Organization: sourced("", ctx.Organization, cfg.Organization),
		GitopsRepo:   sourced(config.EnvGitopsRepo, ctx.GitopsRepo, cfg.GitopsRepo),
		TemplateRepo: sourced(config.EnvTemplateRepo, ctx.TemplateRepo, cfg.TemplateRepo),
		ArgocdServer: sourced(config.EnvArgocdServer, ctx.ArgocdServer, cfg.ArgocdServer),
	}, nil
}

var contextShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the active context's resolved settings",
	Long:  "Show the settings in effect for the active customer context, with the source of each value: environment, context, or file. Use --output json for a machine-readable document.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(contextShowOpts.output, "plain", "json"); err != nil {
			return err
		}
		settings, err := resolveShowSettings()
		if err != nil {
			return err
		}
		if contextShowOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(settings)
		}
		ordered := []struct {
			name string
			s    contextSetting
		}{
			{"organization", settings.Organization},
			{"gitopsRepo", settings.GitopsRepo},
			{"templateRepo", settings.TemplateRepo},
			{"argocdServer", settings.ArgocdServer},
		}
		empty := true
		for _, o := range ordered {
			if o.s.Value == "" {
				continue
			}
			empty = false
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", o.name, o.s.Value, o.s.Source)
		}
		if empty {
			stderr := cmd.ErrOrStderr()
			fmt.Fprintln(stderr, "no settings configured")
			path, err := config.Path()
			if err != nil {
				path = "your config.yaml"
			}
			fmt.Fprintf(stderr, "add settings to %s to get started\n", path)
		}
		return nil
	},
}

func init() {
	f := contextShowCmd.Flags()
	f.StringVarP(&contextShowOpts.output, "output", "o", "plain", flagUsageOutput)
	contextCmd.AddCommand(contextShowCmd)
}
