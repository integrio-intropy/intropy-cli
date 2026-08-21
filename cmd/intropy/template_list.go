package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

// template list prints the template names in the library at a release.
// Fetching lives in internal/template; this file is flag plumbing and output
// formatting.
type templateListFlags struct {
	templateVersion string
	templateRepo    string
	output          string
}

var templateListOpts templateListFlags

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the templates in the Intropy template library",
	Long: "List the names of the templates published in the official Intropy template library at the requested " +
		"release. Names are accepted by `template show` and `int create`. " +
		"Use --output json for a machine-readable document.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(templateListOpts.output, "json", "plain"); err != nil {
			return err
		}
		owner, repo, err := resolveTemplateRepo(templateListOpts.templateRepo)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		result, err := template.List(ctx, template.ListOptions{
			Version:   templateListOpts.templateVersion,
			UserAgent: "intropy-cli/" + version,
			Owner:     owner,
			Repo:      repo,
		})
		if err != nil {
			return err
		}
		if templateListOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		if len(result.Templates) == 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "no templates found in %s/%s@%s\n", result.Owner, result.Repo, result.Version)
			return nil
		}
		for _, name := range result.Templates {
			fmt.Fprintln(cmd.OutOrStdout(), name)
		}
		return nil
	},
}

func init() {
	f := templateListCmd.Flags()
	f.StringVar(&templateListOpts.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&templateListOpts.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringVarP(&templateListOpts.output, "output", "o", "plain", flagUsageOutput)
	templateCmd.AddCommand(templateListCmd)
}
