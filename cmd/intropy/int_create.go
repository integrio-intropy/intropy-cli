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
	outDir            string
	output            string // --output: "json" selects the result document on stdout; any other value is a deprecated alias for --out-dir
	name              string
	templateVersion   string
	version           string // deprecated alias for templateVersion
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
		// --output json is the cross-CLI spelling for a result document on
		// stdout. Any other --output value is the deprecated directory alias
		// from before --out-dir existed; it still works but warns.
		switch intCreateFlags.output {
		case "":
			// not given
		case "json":
			if intCreateFlags.outputJSON != "" && intCreateFlags.outputJSON != "-" {
				return newUsageErrorf("cannot combine --output json with --output-json <path> (use one or the other)")
			}
			intCreateFlags.outputJSON = "-"
		default:
			if intCreateFlags.outDir != "" {
				return newUsageErrorf("cannot combine --output with --out-dir (they are the same flag; --output as a directory is deprecated)")
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: --output as a directory is deprecated; use --out-dir (--output now selects a result format, e.g. --output json)")
			intCreateFlags.outDir = intCreateFlags.output
		}
		if intCreateFlags.outputJSON == "-" && intCreateFlags.output != "json" {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: --output-json - is deprecated; use --output json")
		}
		if err := resolveTemplateVersion(cmd, &intCreateFlags.templateVersion, &intCreateFlags.version); err != nil {
			return err
		}
		outputDir, err := resolveCreateName(intCreateFlags.name, intCreateFlags.outDir, sets)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := template.Create(ctx, template.CreateOptions{
			Template:   args[0],
			OutputDir:  outputDir,
			Version:    intCreateFlags.templateVersion,
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
// output dir. -n is sugar for --set name=<v>; it also defaults --output when
// -o is absent.
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
	// --out-dir is the destination directory. --output json selects the
	// machine-readable result document on stdout, matching every other
	// command in the CLI; any other --output value is treated as the old
	// (deprecated) spelling of --out-dir so existing scripts keep working.
	f.StringVarP(&intCreateFlags.outDir, "out-dir", "o", "", "destination directory (defaults to --name)")
	f.StringVar(&intCreateFlags.output, "output", "", "output format: 'json' writes the result document to stdout (any other value is a deprecated alias for --out-dir)")
	_ = intCreateCmd.MarkFlagDirname("out-dir")
	_ = intCreateCmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"json"}, cobra.ShellCompDirectiveNoFileComp
	})
	f.StringVarP(&intCreateFlags.name, "name", "n", "", "integration name; sets the template's 'name' parameter and, unless --out-dir is set, becomes the output directory")
	registerTemplateVersionFlag(intCreateCmd, &intCreateFlags.templateVersion, &intCreateFlags.version)
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, "disable interactive prompts for missing values")
	f.BoolVar(&intCreateFlags.installSkills, "install-skills", false, "install the Intropy agent skills collection without prompting")
	f.BoolVar(&intCreateFlags.skipInstallSkills, "skip-install-skills", false, "skip the agent skills install without prompting")
	intCreateCmd.MarkFlagsMutuallyExclusive("install-skills", "skip-install-skills")
	f.StringVar(&intCreateFlags.outputJSON, "output-json", "", "write a machine-readable result document to this path (for stdout, use --output json instead of --output-json -)")
	// "output" is in the group so the deprecated alias still satisfies the
	// requirement; the alias copy in RunE handles the value.
	intCreateCmd.MarkFlagsOneRequired("out-dir", "name", "output")
	intCmd.AddCommand(intCreateCmd)
}
