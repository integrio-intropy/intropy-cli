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
	publishes       string
	registryURL     string
}

var intCreateFlags createFlags

var intCreateCmd = &cobra.Command{
	Use:   "create <template>",
	Short: "Create a new integration",
	Long:  "Scaffold a new integration from the official Intropy template library. The positional argument selects which template subdirectory to render (e.g. 'hello-world'). Use --subscribe with message-capable consuming templates to resolve a registry message into the scaffold record's subscribe block; --publishes wires a producing template to a message the registry already declares. Either flag seeds the template's message parameter; the registry is contacted once for resolution and never again.",
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
		if intCreateFlags.subscribe != "" && intCreateFlags.publishes != "" {
			return newUsageErrorf("cannot combine --subscribe with --publishes (one component is one direction)")
		}
		// Explicit message flags resolve before the template fetch, so a wrong
		// or unconfigured registry is reported before a GitHub download.
		loadPromptRefs := !intCreateFlags.noInput && intCreateFlags.subscribe == "" && intCreateFlags.publishes == ""
		messageRefsLoader := promptMessageRefsLoader(loadPromptRefs, intCreateFlags.registryURL, stderr)
		var subscribeBlock *template.SubscribeBlock
		if intCreateFlags.subscribe != "" {
			block, err := resolveSubscribeBlock(cmd.Context(), intCreateFlags.subscribe, intCreateFlags.registryURL, intCreateFlags.noInput, stderr)
			if err != nil {
				return err
			}
			subscribeBlock = block
		}
		var publishesBlock *template.PublishesBlock
		if intCreateFlags.publishes != "" {
			block, err := resolvePublishesBlock(cmd.Context(), intCreateFlags.publishes, intCreateFlags.registryURL)
			if err != nil {
				return err
			}
			publishesBlock = block
		}
		if skipOutDir {
			out, err := deriveOutDir(cmd.Context(), template.CreateOptions{
				Template:          args[0],
				Version:           intCreateFlags.templateVersion,
				SetValues:         sets,
				Files:             intCreateFlags.values,
				NoInput:           intCreateFlags.noInput,
				Stdin:             cmd.InOrStdin(),
				Stdout:            cmd.OutOrStdout(),
				Stderr:            cmd.ErrOrStderr(),
				UserAgent:         "intropy-cli/" + version,
				Owner:             owner,
				Repo:              repo,
				Subscribe:         subscribeBlock,
				Publishes:         publishesBlock,
				MessageRefsLoader: messageRefsLoader,
			})
			if err != nil {
				return usageIfMessageGateError(err)
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
			Template:          args[0],
			OutputDir:         outputDir,
			Version:           intCreateFlags.templateVersion,
			SetValues:         sets,
			Files:             intCreateFlags.values,
			Force:             intCreateFlags.force,
			NoInput:           intCreateFlags.noInput,
			OutputJSON:        outputJSON,
			Stdin:             cmd.InOrStdin(),
			Stdout:            cmd.OutOrStdout(),
			Stderr:            stderr,
			UserAgent:         "intropy-cli/" + version,
			Owner:             owner,
			Repo:              repo,
			Facts:             facts,
			Subscribe:         subscribeBlock,
			Publishes:         publishesBlock,
			MessageRefsLoader: messageRefsLoader,
		}); err != nil {
			return usageIfMessageGateError(err)
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
	f.StringVar(&intCreateFlags.publishes, "publishes", "", flagUsagePublishes)
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
	client, err := newXRegistryClient(url)
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

// resolvePublishesBlock turns --publishes <ref> into the record block:
// the same registry walk as --subscribe, but the component under scaffold
// is the producer, so the producing channel is not a choice. Every
// channel on the message belongs to an existing producer; subscribing to
// their pick would make this scaffold a second producer on someone
// else's channel. Zero producers can only mean the endpoint is not
// registered yet: the remediation names that, not a CLI fix.
func resolvePublishesBlock(ctx context.Context, ref, registryURLFlag string) (*template.PublishesBlock, error) {
	url, err := resolveRegistryURL(registryURLFlag)
	if err != nil {
		return nil, err
	}
	client, err := newXRegistryClient(url)
	if err != nil {
		return nil, err
	}
	res, err := client.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	channel, pickErr := res.SubscribeChannel()
	if pickErr != nil {
		var amb *xregistry.AmbiguousProducerError
		if errors.As(pickErr, &amb) {
			return nil, fmt.Errorf("%w\na new producer cannot pick among existing channels; register the endpoint for this component in the registry first", amb)
		}
		var no *xregistry.NoProducerError
		if errors.As(pickErr, &no) {
			return nil, fmt.Errorf("%w\nregister the producing endpoint for this component in the registry before scaffolding it", no)
		}
		return nil, pickErr
	}
	return &template.PublishesBlock{
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

// promptMessageRefsLoader loads registry candidates only after the fetched
// manifest proves a template can use them. Errors propagate: the create
// flow degrades loader failures to a warning beside the workspace-only
// candidate pool, and explicit message flags keep their hard-failure path
// before this loader is ever invoked. An unconfigured registry gets its
// own warning — an empty pool should read as "no registry", not "no
// messages exist".
func promptMessageRefsLoader(enabled bool, registryURLFlag string, stderr io.Writer) func(context.Context) ([]string, error) {
	if !enabled {
		return nil
	}
	var (
		loaded bool
		refs   []string
		err    error
	)
	return func(ctx context.Context) ([]string, error) {
		if loaded {
			return refs, nil
		}
		loaded = true
		if cfg, err := config.Load(); err == nil {
			if cfg.Resolve(config.Flags{RegistryURL: registryURLFlag}).RegistryURL == "" {
				fmt.Fprintf(stderr, "warning: no registryUrl configured — message suggestions come from workspace messages only\n")
				return nil, nil
			}
		}
		refs, err = registryMessageRefs(ctx, registryURLFlag)
		return refs, err
	}
}

// registryMessageRefs lists every configured registry message ref for
// prompt suggestions. An unconfigured registry is the empty candidate pool
// — the loader warns before reaching here; internal workspace messages
// join through the facts, not here.
func registryMessageRefs(ctx context.Context, registryURLFlag string) ([]string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	url := cfg.Resolve(config.Flags{RegistryURL: registryURLFlag}).RegistryURL
	if url == "" {
		return nil, nil
	}
	client, err := newXRegistryClient(url)
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

// usageIfMessageGateError maps invalid message-flag/template combinations
// to usage errors so they exit 2, not 1.
func usageIfMessageGateError(err error) error {
	if errors.Is(err, template.ErrNoMessageParameters) ||
		errors.Is(err, template.ErrSubscribeMessageConflict) ||
		errors.Is(err, template.ErrMessageParameterConflict) ||
		errors.Is(err, template.ErrMessageDirection) ||
		errors.Is(err, template.ErrMessageFlagsExclusive) {
		return newUsageErrorf("%v", err)
	}
	return err
}
