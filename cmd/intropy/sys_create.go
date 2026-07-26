package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type sysCreateFlags struct {
	name       string
	output     string
	version    string
	force      bool
	outputJSON string
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
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return system.Create(ctx, system.CreateOptions{
			Name:       sysCreateFlagValues.name,
			OutputDir:  sysCreateFlagValues.output,
			Version:    sysCreateFlagValues.version,
			Force:      sysCreateFlagValues.force,
			OutputJSON: sysCreateFlagValues.outputJSON,
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
		})
	},
}

func init() {
	f := sysCreateCmd.Flags()
	f.StringVarP(&sysCreateFlagValues.name, "name", "n", "", "system name; PascalCase or kebab-case (OrderFlow and order-flow are equivalent)")
	f.StringVarP(&sysCreateFlagValues.output, "output", "o", "", "destination directory (default: the kebab-cased name)")
	f.StringVar(&sysCreateFlagValues.version, "version", "", "system-host template release tag (default: latest)")
	f.BoolVar(&sysCreateFlagValues.force, "force", false, "allow rendering into a non-empty output directory")
	f.StringVar(&sysCreateFlagValues.outputJSON, "output-json", "", "write a machine-readable result document to this path (- for stdout)")
	_ = sysCreateCmd.MarkFlagRequired("name")
	sysCmd.AddCommand(sysCreateCmd)
}
