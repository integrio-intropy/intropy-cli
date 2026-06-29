package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/blueprint"
	"github.com/spf13/cobra"
)

type createFlags struct {
	output     string
	version    string
	values     []string
	sets       []string
	force      bool
	noInput    bool
	outputJSON string
}

var intCreateFlags createFlags

var intCreateCmd = &cobra.Command{
	Use:               "create <blueprint>",
	Short:             "Create a new integration",
	Long:              "Scaffold a new integration from the official Intropy blueprints library. The positional argument selects which blueprint subdirectory to render (e.g. 'hello-world').",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBlueprints,
	RunE: func(cmd *cobra.Command, args []string) error {
		sets, err := blueprint.ParseSets(intCreateFlags.sets)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return blueprint.Create(ctx, blueprint.CreateOptions{
			Blueprint:  args[0],
			OutputDir:  intCreateFlags.output,
			Version:    intCreateFlags.version,
			SetValues:  sets,
			Files:      intCreateFlags.values,
			Force:      intCreateFlags.force,
			NoInput:    intCreateFlags.noInput,
			OutputJSON: intCreateFlags.outputJSON,
			Stdin:      cmd.InOrStdin(),
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
		})
	},
}

func init() {
	f := intCreateCmd.Flags()
	f.StringVarP(&intCreateFlags.output, "output", "o", "", "destination directory (required)")
	f.StringVar(&intCreateFlags.version, "version", "", "blueprint release tag (default: latest)")
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, "disable interactive prompts for missing values")
	f.StringVar(&intCreateFlags.outputJSON, "output-json", "", "write a machine-readable result document to this path (- for stdout)")
	_ = intCreateCmd.MarkFlagRequired("output")
	intCmd.AddCommand(intCreateCmd)
}
