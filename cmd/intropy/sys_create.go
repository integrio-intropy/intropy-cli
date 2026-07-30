package main

import (
	"os"
	"os/signal"
	"syscall"

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
	force           bool
}

var sysCreateFlagValues sysCreateFlags

var sysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Assemble scaffolded integrations into a system host",
	Long: "Scan the workspace for integration scaffold records (" + template.ScaffoldRelPath + "), render the system-host template, " +
		"and assemble the typed system declaration — Topics.cs and the ISystemDefinition class — from what the scaffolds recorded. " +
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
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return system.Create(ctx, system.CreateOptions{
			Name:       sysCreateFlagValues.name,
			OutputDir:  sysCreateFlagValues.outDir,
			Version:    sysCreateFlagValues.templateVersion,
			Force:      sysCreateFlagValues.force,
			OutputJSON: outputJSON,
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
		})
	},
}

func init() {
	f := sysCreateCmd.Flags()
	f.StringVarP(&sysCreateFlagValues.name, "name", "n", "", "system name; PascalCase or kebab-case (OrderFlow and order-flow are equivalent)")
	f.StringVarP(&sysCreateFlagValues.outDir, "out-dir", "o", "", "destination directory (default: the kebab-cased name)")
	f.StringVar(&sysCreateFlagValues.output, "output", "", flagUsageOutputJSONOnly)
	_ = sysCreateCmd.MarkFlagDirname("out-dir")
	f.StringVar(&sysCreateFlagValues.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.BoolVar(&sysCreateFlagValues.force, "force", false, "allow rendering into a non-empty output directory")
	_ = sysCreateCmd.MarkFlagRequired("name")
	sysCmd.AddCommand(sysCreateCmd)
}
