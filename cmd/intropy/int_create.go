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
	name       string
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
		outputDir, err := resolveCreateName(intCreateFlags.name, intCreateFlags.output, sets)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return blueprint.Create(ctx, blueprint.CreateOptions{
			Blueprint:  args[0],
			OutputDir:  outputDir,
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

// resolveCreateName folds the -n shorthand into the set map and derives the
// output dir. -n is sugar for --set name=<v>; it also defaults --output.
func resolveCreateName(name, output string, sets map[string]any) (string, error) {
	if name == "" {
		return output, nil
	}
	if _, ok := sets["name"]; ok {
		return "", newUsageErrorf("cannot combine --name with --set name= (they conflict)")
	}
	sets["name"] = name
	if output == "" {
		output = name
	}
	return output, nil
}

func init() {
	f := intCreateCmd.Flags()
	f.StringVarP(&intCreateFlags.output, "output", "o", "", "destination directory (defaults to --name)")
	f.StringVarP(&intCreateFlags.name, "name", "n", "", "integration name; sets the blueprint's 'name' parameter and, unless -o is set, becomes the output directory")
	f.StringVar(&intCreateFlags.version, "version", "", "blueprint release tag (default: latest)")
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, "disable interactive prompts for missing values")
	f.StringVar(&intCreateFlags.outputJSON, "output-json", "", "write a machine-readable result document to this path (- for stdout)")
	intCreateCmd.MarkFlagsOneRequired("output", "name")
	intCmd.AddCommand(intCreateCmd)
}
