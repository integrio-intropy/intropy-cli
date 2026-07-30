package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type sysCreateFlags struct {
	name       string
	outDir     string
	output     string // --output: "json" selects the result document on stdout; any other value is a deprecated alias for --out-dir
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
		// Same --output split as int create: "json" is the result document on
		// stdout, anything else is the deprecated --out-dir alias.
		switch sysCreateFlagValues.output {
		case "":
		case "json":
			if sysCreateFlagValues.outputJSON != "" && sysCreateFlagValues.outputJSON != "-" {
				return newUsageErrorf("cannot combine --output json with --output-json <path> (use one or the other)")
			}
			sysCreateFlagValues.outputJSON = "-"
		default:
			if sysCreateFlagValues.outDir != "" {
				return newUsageErrorf("cannot combine --output with --out-dir (they are the same flag; --output as a directory is deprecated)")
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: --output as a directory is deprecated; use --out-dir (--output now selects a result format, e.g. --output json)")
			sysCreateFlagValues.outDir = sysCreateFlagValues.output
		}
		if sysCreateFlagValues.outputJSON == "-" && sysCreateFlagValues.output != "json" {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: --output-json - is deprecated; use --output json")
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return system.Create(ctx, system.CreateOptions{
			Name:       sysCreateFlagValues.name,
			OutputDir:  sysCreateFlagValues.outDir,
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
	// --out-dir is the destination directory. --output json selects the
	// machine-readable result document on stdout, matching every other
	// command in the CLI; any other --output value is treated as the old
	// (deprecated) spelling of --out-dir so existing scripts keep working.
	f.StringVarP(&sysCreateFlagValues.outDir, "out-dir", "o", "", "destination directory (default: the kebab-cased name)")
	f.StringVar(&sysCreateFlagValues.output, "output", "", "output format: 'json' writes the result document to stdout (any other value is a deprecated alias for --out-dir)")
	_ = sysCreateCmd.MarkFlagDirname("out-dir")
	_ = sysCreateCmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"json"}, cobra.ShellCompDirectiveNoFileComp
	})
	f.StringVar(&sysCreateFlagValues.version, "version", "", "system-host template release tag (default: latest)")
	f.BoolVar(&sysCreateFlagValues.force, "force", false, "allow rendering into a non-empty output directory")
	f.StringVar(&sysCreateFlagValues.outputJSON, "output-json", "", "write a machine-readable result document to this path (for stdout, use --output json instead of --output-json -)")
	_ = sysCreateCmd.MarkFlagRequired("name")
	sysCmd.AddCommand(sysCreateCmd)
}
