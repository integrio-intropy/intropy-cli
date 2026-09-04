package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/huandu/xstrings"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/xregistry"
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
	subscribe       string
	registryURL     string
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
		stderr := cmd.ErrOrStderr()
		// The subscribe rung resolves before the template fetch, so a wrong
		// or unconfigured registry is reported before a GitHub download.
		downloadRefs := !intCreateFlags.noInput && intCreateFlags.subscribe == ""
		var subscribeBlock *template.SubscribeBlock
		if intCreateFlags.subscribe != "" {
			block, err := resolveSubscribeBlock(cmd.Context(), intCreateFlags.subscribe, intCreateFlags.registryURL, intCreateFlags.noInput, stderr)
			if err != nil {
				return err
			}
			subscribeBlock = block
		}
		var messageRefs []string
		if downloadRefs {
			// Prompt-time candidates, best effort: an unreachable registry
			// degrades to facts-only suggestions. With --subscribe set the
			// same failure fails hard, inside resolveSubscribeBlock.
			refs, err := registryMessageRefs(cmd.Context(), intCreateFlags.registryURL)
			if err != nil {
				var ue *usageError
				if errors.As(err, &ue) {
					return err
				}
				fmt.Fprintf(stderr, "warning: %v — prompt suggestions fall back to workspace messages only\n", err)
			} else {
				messageRefs = refs
			}
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
				Subscribe: subscribeBlock,
			})
			if err != nil {
				return err
			}
			outputDir = out
		}
		facts, warnings := system.LoadWorkspaceFacts(workspaceRootOf(outputDir))
		for _, w := range warnings {
			printWarning(stderr, w)
		}
		seedOrganization(facts)
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := template.Create(ctx, template.CreateOptions{
			Template:    args[0],
			OutputDir:   outputDir,
			Version:     intCreateFlags.templateVersion,
			SetValues:   sets,
			Files:       intCreateFlags.values,
			Force:       intCreateFlags.force,
			NoInput:     intCreateFlags.noInput,
			OutputJSON:  outputJSON,
			Stdin:       cmd.InOrStdin(),
			Stdout:      cmd.OutOrStdout(),
			Stderr:      stderr,
			UserAgent:   "intropy-cli/" + version,
			Owner:       owner,
			Repo:        repo,
			Facts:       facts,
			Subscribe:   subscribeBlock,
			MessageRefs: messageRefs,
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
	f.StringVar(&intCreateFlags.subscribe, "subscribe", "", flagUsageSubscribe)
	f.StringVar(&intCreateFlags.registryURL, "registry-url", "", flagUsageRegistryURL)
	intCmd.AddCommand(intCreateCmd)
}

// resolveSubscribeBlock turns --subscribe <ref> into the record block:
// resolve the message against the registry, pin its schema's default
// version URL, and pick the producing channel. An ambiguous channel is an
// interactive pick; under --no-input it is an error listing the
// candidates. Every failure here is hard: an explicit --subscribe asked
// for this wiring.
func resolveSubscribeBlock(ctx context.Context, ref, registryURLFlag string, noInput bool, stderr io.Writer) (*template.SubscribeBlock, error) {
	url, err := resolveRegistryURL(registryURLFlag)
	if err != nil {
		return nil, err
	}
	client, err := xregistry.New(url, xregistry.WithUserAgent("intropy-cli/"+version))
	if err != nil {
		return nil, err
	}
	res, err := client.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	channel, err := pickSubscribeChannel(res, noInput, stderr)
	if err != nil {
		return nil, err
	}
	return &template.SubscribeBlock{
		Message:       res.Message,
		Pubsub:        channel.Pubsub,
		Topic:         channel.Topic,
		Dataschema:    res.DataSchema,
		DataschemaURL: res.DataSchemaURL,
	}, nil
}

// pickSubscribeChannel chooses among the resolved message's producing
// channels: one is taken, several prompt, several under --no-input fail
// naming every candidate.
func pickSubscribeChannel(res *xregistry.ResolvedMessage, noInput bool, stderr io.Writer) (xregistry.Channel, error) {
	channel, pickErr := res.SubscribeChannel()
	if pickErr == nil {
		return channel, nil
	}
	var amb *xregistry.AmbiguousProducerError
	if !errors.As(pickErr, &amb) || noInput {
		return channel, pickErr
	}
	prompter := template.AutoPrompter(os.Stdin, stderr, false)
	if prompter == nil {
		return channel, pickErr
	}
	suggestions := make([]string, 0, len(amb.Channels))
	for _, c := range amb.Channels {
		suggestions = append(suggestions, c.Pubsub+"/"+c.Topic)
	}
	field := template.FieldSpec{
		Name:        "channel",
		Title:       fmt.Sprintf("message %s is produced on several channels; pick one", res.Message),
		Type:        "string",
		Suggestions: suggestions,
	}
	ans, _, err := prompter.Prompt(field)
	if err != nil {
		return channel, err
	}
	picked, _ := ans.(string)
	for _, c := range amb.Channels {
		if c.Pubsub+"/"+c.Topic == picked {
			return c, nil
		}
	}
	return channel, fmt.Errorf("picked channel %q is not one of: %s", picked, strings.Join(suggestions, ", "))
}

// registryMessageRefs lists every registry message ref for prompt
// suggestions. Internal (workspace) messages join them via the facts, not
// here.
func registryMessageRefs(ctx context.Context, registryURLFlag string) ([]string, error) {
	url, err := resolveRegistryURL(registryURLFlag)
	if err != nil {
		return nil, err
	}
	client, err := xregistry.New(url, xregistry.WithUserAgent("intropy-cli/"+version))
	if err != nil {
		return nil, err
	}
	doc, err := client.Export(ctx)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, g := range doc.MessageGroups {
		for id := range g.Messages {
			refs = append(refs, id)
		}
	}
	sort.Strings(refs)
	return refs, nil
}

// usageIfNoMessageParams maps the --subscribe gate to a usage error so an
// unsupported template/request combination exits 2, not 1.
func usageIfNoMessageParams(err error) error {
	if errors.Is(err, template.ErrNoMessageParameters) {
		return newUsageErrorf("%v", err)
	}
	return err
}
