package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

// template show prints a template's manifest. Fetching and parsing the
// manifest live in internal/template; this file is flag plumbing and output
// formatting.
type templateShowFlags struct {
	templateVersion string
	templateRepo    string
	output          string
}

var templateShowOpts templateShowFlags

var templateShowCmd = &cobra.Command{
	Use:   "show <template>",
	Short: "Show a template's manifest and parameter schema",
	Long: "Print the template manifest — metadata and parameter schema — for the requested release. " +
		"Use --output json to emit a stable, machine-readable document (the same schema Backstage's frontend renders).",
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(templateShowOpts.output, "json", "plain"); err != nil {
			return err
		}
		owner, repo, err := resolveTemplateRepo(templateShowOpts.templateRepo)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		result, err := template.Describe(ctx, template.DescribeOptions{
			Template:  args[0],
			Version:   templateShowOpts.templateVersion,
			UserAgent: "intropy-cli/" + version,
			Owner:     owner,
			Repo:      repo,
		})
		if err != nil {
			return err
		}
		if templateShowOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		result.FormatText(cmd.OutOrStdout())
		return nil
	},
}

func init() {
	f := templateShowCmd.Flags()
	f.StringVar(&templateShowOpts.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&templateShowOpts.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringVarP(&templateShowOpts.output, "output", "o", "plain", flagUsageOutput)
	templateCmd.AddCommand(templateShowCmd)
}
