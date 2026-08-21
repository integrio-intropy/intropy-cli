package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/spf13/cobra"
)

// sys update folds scaffolded integrations the system host does not yet
// declare into its rendered definitions. The diff and render live in
// internal/system.Update; this file is flag plumbing.
type sysUpdateFlags struct {
	output          string
	dryRun          bool
	force           bool
	templateVersion string
	templateRepo    string
}

var sysUpdateFlagValues sysUpdateFlags

var sysUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Fold orphaned components into the system host",
	Long: "Scan the workspace for integration scaffold records the system host does not declare, and re-render the host's " +
		"declaration files to include them. The host renders with the template pin its scaffold record stores. " +
		"An identical file is kept, a missing file is written, and a differing file is an error unless --force. " +
		"A declared component is never removed, even when its scaffold has disappeared. " +
		"Run it from the workspace root that contains the host and its sibling components.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sysUpdateFlagValues.output != "" && sysUpdateFlagValues.output != "json" {
			return newUsageErrorf("invalid output format %q (allowed: json)", sysUpdateFlagValues.output)
		}
		outputJSON := ""
		if sysUpdateFlagValues.output == "json" {
			outputJSON = "-"
		}
		owner, repo, err := resolveTemplateRepo(sysUpdateFlagValues.templateRepo)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return system.Update(ctx, system.UpdateOptions{
			Force:      sysUpdateFlagValues.force,
			DryRun:     sysUpdateFlagValues.dryRun,
			OutputJSON: outputJSON,
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
			Owner:      owner,
			Repo:       repo,
			Version:    sysUpdateFlagValues.templateVersion,
		})
	},
}

func init() {
	f := sysUpdateCmd.Flags()
	f.StringVar(&sysUpdateFlagValues.output, "output", "", flagUsageOutputJSONOnly)
	f.BoolVar(&sysUpdateFlagValues.dryRun, "dry-run", false, "print the update plan without writing files or the scaffold record")
	f.BoolVar(&sysUpdateFlagValues.force, "force", false, "overwrite declaration files that differ from the rendered update")
	f.StringVar(&sysUpdateFlagValues.templateVersion, "template-version", "", "override the template release tag the host record pins")
	f.StringVar(&sysUpdateFlagValues.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	sysCmd.AddCommand(sysUpdateCmd)
}
