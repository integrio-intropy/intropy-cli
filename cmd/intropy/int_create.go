package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type createFlags struct {
	output            string
	name              string
	version           string
	values            []string
	sets              []string
	force             bool
	noInput           bool
	outputJSON        string
	installSkills     bool
	skipInstallSkills bool
}

var intCreateFlags createFlags

var intCreateCmd = &cobra.Command{
	Use:               "create <template>",
	Short:             "Create a new integration",
	Long:              "Scaffold a new integration from the official Intropy template library. The positional argument selects which template subdirectory to render (e.g. 'hello-world'). After scaffolding, offers to install the Intropy agent skills collection into the new integration; --install-skills installs and --skip-install-skills skips without prompting, otherwise the prompt is skipped with --no-input or when stdin is not a terminal.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeTemplates,
	RunE: func(cmd *cobra.Command, args []string) error {
		sets, err := template.ParseSets(intCreateFlags.sets)
		if err != nil {
			return err
		}
		outputDir, err := resolveCreateName(intCreateFlags.name, intCreateFlags.output, sets)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := template.Create(ctx, template.CreateOptions{
			Template:   args[0],
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
		}); err != nil {
			return err
		}
		if err := maybeInstallSkills(ctx, cmd.InOrStdin(), cmd.ErrOrStderr(), intCreateFlags.installSkills, intCreateFlags.skipInstallSkills, intCreateFlags.noInput, outputDir); err != nil {
			return fmt.Errorf("integration created, but skills install failed: %w", err)
		}
		return nil
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
	f.StringVarP(&intCreateFlags.name, "name", "n", "", "integration name; sets the template's 'name' parameter and, unless -o is set, becomes the output directory")
	f.StringVar(&intCreateFlags.version, "version", "", "template release tag (default: latest)")
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, "disable interactive prompts for missing values")
	f.BoolVar(&intCreateFlags.installSkills, "install-skills", false, "install the Intropy agent skills collection without prompting")
	f.BoolVar(&intCreateFlags.skipInstallSkills, "skip-install-skills", false, "skip the agent skills install without prompting")
	intCreateCmd.MarkFlagsMutuallyExclusive("install-skills", "skip-install-skills")
	f.StringVar(&intCreateFlags.outputJSON, "output-json", "", "write a machine-readable result document to this path (- for stdout)")
	intCreateCmd.MarkFlagsOneRequired("output", "name")
	intCmd.AddCommand(intCreateCmd)
}
