package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/deploy"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type manifestsCreateFlags struct {
	output     string
	version    string
	values     []string
	sets       []string
	force      bool
	noInput    bool
	outputJSON string
}

var manifestsCreateFlagValues manifestsCreateFlags

var manifestsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create Kubernetes manifests for a scaffolded integration",
	Long: "Generate a kustomize base + overlays tree of Kubernetes deployment manifests for a previously scaffolded integration. " +
		"Run it inside the integration project: the command walks up from the current directory to find " + template.ScaffoldRelPath + ", " +
		"re-fetches the exact template version recorded there, and renders its manifests/ templates. " +
		"Scaffold values (e.g. name, appPort) pre-fill matching manifest parameters; remaining required parameters are prompted for, or supplied via --set/--values.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sets, err := template.ParseSets(manifestsCreateFlagValues.sets)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return deploy.Create(ctx, deploy.CreateOptions{
			OutputDir:  manifestsCreateFlagValues.output,
			Version:    manifestsCreateFlagValues.version,
			SetValues:  sets,
			Files:      manifestsCreateFlagValues.values,
			Force:      manifestsCreateFlagValues.force,
			NoInput:    manifestsCreateFlagValues.noInput,
			OutputJSON: manifestsCreateFlagValues.outputJSON,
			Stdin:      cmd.InOrStdin(),
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			UserAgent:  "intropy-cli/" + version,
		})
	},
}

func init() {
	f := manifestsCreateCmd.Flags()
	f.StringVarP(&manifestsCreateFlagValues.output, "output", "o", "", "destination directory (default: deploy, relative to the project root)")
	f.StringVar(&manifestsCreateFlagValues.version, "version", "", "template release tag (default: the version pinned in "+template.ScaffoldRelPath+")")
	f.StringArrayVarP(&manifestsCreateFlagValues.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&manifestsCreateFlagValues.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&manifestsCreateFlagValues.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&manifestsCreateFlagValues.noInput, "no-input", false, "disable interactive prompts for missing values")
	f.StringVar(&manifestsCreateFlagValues.outputJSON, "output-json", "", "write a machine-readable result document to this path (- for stdout)")
	manifestsCmd.AddCommand(manifestsCreateCmd)
}
