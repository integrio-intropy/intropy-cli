package main

import (
	"context"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

// sys create assembles scaffolded integrations into a system host. The
// scan, render, and assembly live in internal/system.Create; this file is
// flag plumbing.
type sysCreateFlags struct {
	name            string
	outDir          string
	output          string
	templateVersion string
	templateRepo    string
	force           bool
}

var sysCreateFlagValues sysCreateFlags

var sysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Assemble scaffolded integrations into a system host",
	Long: "Scan the workspace for integration scaffold records (" + template.ScaffoldRelPath + "), validate them into a system model, " +
		"and render the system-host template with the assembled values. The template writes the full declaration — Topics.cs, Ports.cs, " +
		"the development and system definitions, and the project file; the CLI supplies only what the workspace records. " +
		"Run it from the workspace root that contains the scaffolded components and the shared contracts project.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sysCreateFlagValues.output != "" && sysCreateFlagValues.output != "json" {
			return newUsageErrorf("invalid output format %q (allowed: json)", sysCreateFlagValues.output)
		}
		outputJSON := ""
		if sysCreateFlagValues.output == "json" {
			outputJSON = "-"
		}
		owner, repo, err := resolveTemplateRepo(sysCreateFlagValues.templateRepo)
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(cmd)
		defer cancel()
		return system.Create(ctx, system.CreateOptions{
			Name:             sysCreateFlagValues.name,
			OutputDir:        sysCreateFlagValues.outDir,
			Version:          sysCreateFlagValues.templateVersion,
			Force:            sysCreateFlagValues.force,
			ConfirmOverwrite: confirmHostOverwrite(ctx, cmd),
			OutputJSON:       outputJSON,
			Stdout:           cmd.OutOrStdout(),
			Stderr:           cmd.ErrOrStderr(),
			UserAgent:        userAgent(),
			Owner:            owner,
			Repo:             repo,
		})
	},
}

func init() {
	f := sysCreateCmd.Flags()
	f.StringVarP(&sysCreateFlagValues.name, "name", "n", "", "system name; PascalCase or kebab-case (OrderFlow and order-flow are equivalent)")
	f.StringVarP(&sysCreateFlagValues.outDir, "out-dir", "o", "", "destination directory (defaults to the kebab-cased name)")
	f.StringVar(&sysCreateFlagValues.output, "output", "", flagUsageOutputJSONOnly)
	_ = sysCreateCmd.MarkFlagDirname("out-dir")
	f.StringVar(&sysCreateFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&sysCreateFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.BoolVar(&sysCreateFlagValues.force, "force", false, "overwrite a non-empty output directory, replacing hand edits (asks first on a terminal)")
	_ = sysCreateCmd.MarkFlagRequired("name")
	sysCmd.AddCommand(sysCreateCmd)
}

// confirmHostOverwrite asks before --force re-renders an existing host, which
// replaces hand edits to its declaration files. Without a terminal it cannot
// ask, so it warns and proceeds: scripts that pass --force keep working.
func confirmHostOverwrite(ctx context.Context, cmd *cobra.Command) func(string) (bool, error) {
	return func(dir string) (bool, error) {
		warning := fmt.Sprintf("--force overwrites every file in %s the template renders; hand edits to them, such as routing in Topics.cs and Ports.cs, are lost", dir)
		hint := "to add new components and keep hand edits, run 'intropy sys update' instead"
		confirmer := interactive.NewTerminalConfirmer(cmd.InOrStdin(), cmd.ErrOrStderr())
		if confirmer == nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n%s\n", warning, hint)
			return true, nil
		}
		return confirmer.Confirm(ctx, interactive.ConfirmRequest{
			Title:       fmt.Sprintf("Overwrite %s?", dir),
			Description: warning + ".\n" + hint + ".",
		})
	}
}
