package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/huandu/xstrings"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/system"
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
	Args:  usageArgs(cobra.ExactArgs(1)),
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
		// With no --name the output dir falls to the resolved values: a
		// template with a "name" parameter gets the kebab-cased value, the
		// same convention --name itself defaults by.
		skipOutDir := intCreateFlags.outDir == "" && intCreateFlags.name == ""
		outputDir, err := resolveCreateName(intCreateFlags.name, intCreateFlags.outDir, sets)
		if err != nil {
			return err
		}
		owner, repo, err := resolveTemplateRepo(intCreateFlags.templateRepo)
		if err != nil {
			return err
		}
		if skipOutDir {
			out, err := deriveOutDir(cmd.Context(), template.CreateOptions{
				Template:  args[0],
				Version:   intCreateFlags.templateVersion,
				SetValues: sets,
				Files:     intCreateFlags.values,
				NoInput:   intCreateFlags.noInput,
				Stdin:     cmd.InOrStdin(),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				UserAgent: "intropy-cli/" + version,
				Owner:     owner,
				Repo:      repo,
			})
			if err != nil {
				return err
			}
			outputDir = out
		}
		stderr := cmd.ErrOrStderr()
		facts, warnings := system.LoadWorkspaceFacts(workspaceRootOf(outputDir))
		for _, w := range warnings {
			printWarning(stderr, w)
		}
		seedOrganization(facts)
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
			Stderr:     stderr,
			UserAgent:  "intropy-cli/" + version,
			Owner:      owner,
			Repo:       repo,
			Facts:      facts,
		}); err != nil {
			return err
		}
		return nil
	},
}

// seedOrganization feeds the resolved config's organization into the
// facts as the ambient default. A workspace whose records already agree
// on an organization keeps its own — specific beats ambient. A config
// that cannot be read contributes nothing rather than failing the
// create: a missing config file is the common case, and the prompt is
// the fallback the fact would have spared.
func seedOrganization(facts *template.WorkspaceFacts) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	facts.SetOrganization(cfg.Resolve(config.Flags{}).Organization)
}

// workspaceRootOf derives the directory whose scaffold records feed
// prompt-time suggestions: the parent of the output directory, which is
// where a flat workspace keeps the new component's siblings.
func workspaceRootOf(outputDir string) string {
	if outputDir == "" {
		return "."
	}
	return filepath.Dir(outputDir)
}

// printWarning reports a workspace-scan issue without failing the create —
// a malformed sibling record must not block scaffolding a new component.
func printWarning(stderr io.Writer, w error) {
	fmt.Fprintf(stderr, "warning: %v\n", w)
}

// resolveCreateName folds the -n shorthand into the set map and derives the
// output dir. -n is sugar for --set name=<v>; it also defaults the output
// dir to the kebab-cased name when -o is absent — the same normalization
// sys create applies, so a name and its kebab form are one component.
func resolveCreateName(name, output string, sets map[string]any) (string, error) {
	if name == "" {
		return output, nil
	}
	if _, ok := sets["name"]; ok {
		return "", newUsageErrorf("cannot combine --name with --set name= (they conflict)")
	}
	sets["name"] = name
	if output == "" {
		output = xstrings.ToKebabCase(name)
	}
	return output, nil
}

// deriveOutDir runs the create far enough to resolve values and returns
// the kebab-cased "name" value — the directory convention for runs that
// name neither --name nor --out-dir. A template with no "name" parameter
// leaves the run with nothing to derive from, the usage error --out-dir
// would have spared.
func deriveOutDir(ctx context.Context, opts template.CreateOptions) (string, error) {
	prep, err := template.PrepareCreate(ctx, opts)
	if err != nil {
		return "", err
	}
	defer prep.Cleanup()
	name, ok := prep.Values["name"].(string)
	if !ok || name == "" {
		return "", newUsageErrorf("--out-dir is required (template %q declares no 'name' parameter to derive it from)", opts.Template)
	}
	return xstrings.ToKebabCase(name), nil
}

func init() {
	f := intCreateCmd.Flags()
	f.StringVarP(&intCreateFlags.outDir, "out-dir", "o", "", "destination directory (defaults to the kebab-cased name)")
	f.StringVar(&intCreateFlags.output, "output", "", flagUsageOutputJSONOnly)
	_ = intCreateCmd.MarkFlagDirname("out-dir")
	f.StringVarP(&intCreateFlags.name, "name", "n", "", "integration name; sets the template's 'name' parameter and, unless --out-dir is set, becomes the output directory")
	f.StringVar(&intCreateFlags.templateVersion, "template-version", "", flagUsageTemplateVer)
	f.StringVar(&intCreateFlags.templateRepo, "template-repo", "", flagUsageTemplateRepo)
	f.StringArrayVarP(&intCreateFlags.values, "values", "f", nil, "values file in YAML/JSON (repeatable; use - to read one doc from stdin)")
	f.StringArrayVarP(&intCreateFlags.sets, "set", "s", nil, "set a value as key=value (repeatable)")
	f.BoolVar(&intCreateFlags.force, "force", false, "allow rendering into a non-empty output directory")
	f.BoolVar(&intCreateFlags.noInput, "no-input", false, flagUsageNoInput)
	intCmd.AddCommand(intCreateCmd)
}
