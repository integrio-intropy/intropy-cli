package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type createFlags struct {
	outDir          string
	output          string
	name            string
	templateVersion string
	templateRepo    string
	values          []string
	sets            []string
	force           bool
	noInput         bool
}

var intCreateFlags createFlags

var intCreateCmd = &cobra.Command{
	Use:   "create <template>",
	Short: "Create a new integration",
	Long:  "Scaffold a new integration from the official Intropy template library. The positional argument selects which template subdirectory to render (e.g. 'hello-world').",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sets, err := template.ParseSets(intCreateFlags.sets)
		if err != nil {
			return err
		}
		if intCreateFlags.output != "" && intCreateFlags.output != "json" {
			return newUsageErrorf("invalid output format %q (allowed: json)", intCreateFlags.output)
		}
		outputJSON := ""
		if intCreateFlags.output == "json" {
			outputJSON = "-"
		}
		outputDir, err := resolveCreateName(intCreateFlags.name, intCreateFlags.outDir, sets)
		if err != nil {
			return err
		}
		owner, repo, err := resolveTemplateRepo(intCreateFlags.templateRepo)
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
			OutputJSON: outputJSON,
			Stdin:      cmd.InOrStdin(),
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
			Owner:      owner,
			Repo:       repo,
		}); err != nil {
			return err
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
	f.StringVarP(&intCreateFlags.outDir, "out-dir", "o", "", "destination directory (defaults to --name)")
	f.StringVar(&intCreateFlags.output, "output", "", flagUsageOutputJSONOnly)
	_ = intCreateCmd.MarkFlagDirname("out-dir")
	f.StringVarP(&intCreateFlags.name, "name", "n", "", "integration name; sets the template's 'name' parameter and, unless --out-dir is set, becomes the output directory")
	f.StringVar(&intCreateFlags.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&intCreateFlags.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, flagUsageNoInput)
	intCreateCmd.MarkFlagsOneRequired("out-dir", "name")
	intCmd.AddCommand(intCreateCmd)
}
