package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type CreateOptions struct {
	Template   string // required; subdirectory in the templates repo holding template.yaml + skeleton/
	OutputDir  string
	Version    string
	SetValues  map[string]any
	Files      []string
	Force      bool
	NoInput    bool
	OutputJSON string // path to write CreateResult JSON; "-" means stdout
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	HTTP       *http.Client
	UserAgent  string

	// Owner and Repo select the template library. Zero values target the
	// official library at integrio-intropy/intropy-templates; the CLI sets
	// them from --template-repo, INTROPY_TEMPLATE_REPO, or templateRepo in
	// the config file.
	Owner string
	Repo  string

	// Source carries the fetch seams shared by every template-fetch path.
	// GitHubBaseURL redirects the latest-release API call in tests.
	Source SourceOptions

	// OnManifest, when set, runs after the manifest loads and before
	// values resolve, render, dependency processing, or the scaffold
	// record write. A non-nil error aborts the create. Callers use it for
	// gates that must run before any output is written.
	OnManifest func(*Template) error

	// Facts, when set, feeds prompt-time parameter suggestions: what the
	// workspace's scaffold records already declare (topics, contracts,
	// ports). Facts propose to the prompter; they never resolve a value on
	// their own, and a nil index resolves exactly as before.
	Facts *WorkspaceFacts

	// SubscribeMessage, when set, is the scalar message value written by
	// --subscribe. The manifest must declare a matching message parameter.
	SubscribeMessage string

	// PublishesMessage is SubscribeMessage's producing-side counterpart,
	// written by --publishes.
	PublishesMessage string
}

// CreateResult is the machine-readable summary written when --output-json is
// set. It is the contract chained scaffolder steps consume; field names are
// stable and additive-only.
type CreateResult struct {
	Template  string         `json:"template"`
	Owner     string         `json:"owner"`
	Repo      string         `json:"repo"`
	Version   string         `json:"version"`
	OutputDir string         `json:"outputDir"`
	Values    map[string]any `json:"values"`

	// Dependencies reports what happened to each spec.dependencies entry
	// (including transitive ones): rendered now ("created") or already
	// present ("exists").
	Dependencies []DependencyResult `json:"dependencies,omitempty"`
}

func (o *CreateOptions) applyDefaults() {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
}

// sourceOpts projects the create options onto the shared fetch options. A
// Source that names its own Owner/Repo wins — it is the test seam.
func (o CreateOptions) sourceOpts() SourceOptions {
	s := o.Source
	s.Version = o.Version
	s.Stderr = o.Stderr
	s.UserAgent = o.UserAgent
	if s.Owner == "" && s.Repo == "" {
		s.Owner, s.Repo = libraryIdentity(o.Owner, o.Repo)
	}
	return s
}

// Create runs the full scaffold: resolve release, ensure the cached checkout,
// load manifest, resolve values (with optional interactive prompting), render.
func Create(ctx context.Context, opts CreateOptions) error {
	opts.applyDefaults()
	if err := validateCreateOptions(opts); err != nil {
		return err
	}

	src, err := FetchSource(ctx, opts.sourceOpts())
	if err != nil {
		return err
	}
	tag := src.Version

	templateRoot, err := templateDir(src, src.Owner, src.Repo, opts.Template)
	if err != nil {
		return err
	}

	tmpl, values, err := prepareCreateTemplate(ctx, templateRoot, opts)
	if err != nil {
		return err
	}

	if err := renderCreateOutput(filepath.Join(templateRoot, templateSkeletonDir), tmpl, opts.Template, opts.OutputDir, opts.Force, values); err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "created %s from %s/%s@%s (template %s)\n", opts.OutputDir, src.Owner, src.Repo, tag, opts.Template)

	// Dependencies come from the same library checkout, so they are always
	// version-locked to the component that declared them.
	depRecords, depResults, err := processDependencies(tmpl, values, opts.OutputDir, &depContext{
		repoRoot: filepath.Dir(templateRoot),
		owner:    src.Owner,
		repo:     src.Repo,
		version:  tag,
		stderr:   opts.Stderr,
		visited:  map[string]bool{},
	}, 0)
	if err != nil {
		return err
	}

	// The template field is the repo directory name (opts.Template), not
	// tmpl.Metadata.Name — it is what a later re-fetch needs.
	if err := WriteScaffold(opts.OutputDir, Scaffold{
		SchemaVersion: ScaffoldSchemaVersion,
		Template:      opts.Template,
		Owner:         src.Owner,
		Repo:          src.Repo,
		Version:       tag,
		Values:        values,
		Role:          roleFromLabels(tmpl.Metadata.Labels),
		BlockKind:     blockKindFromLabels(tmpl.Metadata.Labels),
		DataFlow:      dataFlowFromLabels(tmpl.Metadata.Labels),
		DependsOn:     depRecords,
	}); err != nil {
		return err
	}

	return maybeWriteCreateResult(opts, tmpl, values, src, depResults)
}

// routesMessage reports whether a route list names message.
func routesMessage(routes []any, message string) bool {
	for _, r := range routes {
		if m, ok := r.(map[string]any); ok && m[KeyMessage] == message {
			return true
		}
	}
	return false
}

func validateCreateOptions(opts CreateOptions) error {
	if err := validateTemplateName(opts.Template); err != nil {
		return err
	}
	if opts.OutputDir == "" {
		return errors.New("--out-dir is required")
	}
	return nil
}

func prepareCreateTemplate(_ context.Context, templateRoot string, opts CreateOptions) (*Template, map[string]any, error) {
	tmpl, err := LoadTemplate(filepath.Join(templateRoot, templateManifestName))
	if err != nil {
		return nil, nil, err
	}
	if opts.OnManifest != nil {
		if err := opts.OnManifest(tmpl); err != nil {
			return nil, nil, err
		}
	}

	messageParams := tmpl.MessageParameters()
	if len(messageParams) > 0 && opts.Facts != nil {
		opts.Facts.SetMessageParameters(messageParams)
		opts.Facts.SetWiringDirection(MessageDirection(blockKindFromLabels(tmpl.Metadata.Labels)))
	}
	if opts.SubscribeMessage != "" && opts.PublishesMessage != "" {
		return nil, nil, fmt.Errorf("template create: %w", ErrMessageFlagsExclusive)
	}
	// The message blocks land in SetValues alongside the seeds; a caller
	// that passes no sets still gets the block, so nil normalizes here —
	// the one intake point every message flag funnels through.
	if opts.SetValues == nil {
		opts.SetValues = map[string]any{}
	}
	if opts.SubscribeMessage != "" {
		if len(messageParams) == 0 {
			return nil, nil, fmt.Errorf("template %q: %w\n--subscribe requires a template release whose manifest declares message parameters (label %s); check the pinned template version", tmpl.Metadata.Name, ErrNoMessageParameters, TemplateMessageParamsLabel)
		}
		if err := gateMessageDirection(tmpl, MessageDirectionSubscribe); err != nil {
			return nil, nil, err
		}
		if err := seedMessageParameters(tmpl, messageParams, opts.SetValues, "--subscribe", ErrSubscribeMessageConflict, opts.SubscribeMessage); err != nil {
			return nil, nil, err
		}
		opts.SetValues[KeySubscribes] = MessageValue(opts.SubscribeMessage)
	}
	if opts.PublishesMessage != "" {
		if len(messageParams) == 0 {
			return nil, nil, fmt.Errorf("template %q: %w\n--publishes requires a template release whose manifest declares message parameters (label %s); check the pinned template version", tmpl.Metadata.Name, ErrNoMessageParameters, TemplateMessageParamsLabel)
		}
		if err := gateMessageDirection(tmpl, MessageDirectionPublish); err != nil {
			return nil, nil, err
		}
		if err := seedMessageParameters(tmpl, messageParams, opts.SetValues, "--publishes", ErrMessageParameterConflict, opts.PublishesMessage); err != nil {
			return nil, nil, err
		}
		opts.SetValues[KeyPublishes] = MessageValue(opts.PublishesMessage)
	}

	prompter := selectPrompter(&opts)
	values, err := ResolveWith(tmpl, ResolveOptions{
		Facts:    opts.Facts,
		Files:    opts.Files,
		Stdin:    opts.Stdin,
		Sets:     opts.SetValues,
		Prompter: prompter,
		Notes:    opts.Stderr,
	})
	if err != nil {
		return nil, nil, err
	}
	return tmpl, values, nil
}

// renderCreateOutput renders an extracted skeleton into outputDir, honoring
// the manifest's spec.files rules. The caller passes the skeleton root
// itself (PrepareCreate has already verified it), so the missing-skeleton
// check lives next to the fetch that could produce it.
func renderCreateOutput(skelRoot string, tmpl *Template, templateName, outputDir string, force bool, values map[string]any) error {
	if info, err := os.Stat(skelRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("template %q is missing %s/ directory", templateName, templateSkeletonDir)
	}
	if err := ensureOutputDir(outputDir, force); err != nil {
		return err
	}
	return RenderFiltered(skelRoot, outputDir, values, tmpl.Spec.Files)
}

func maybeWriteCreateResult(opts CreateOptions, tmpl *Template, values map[string]any, src *Source, deps []DependencyResult) error {
	if opts.OutputJSON == "" {
		return nil
	}

	absOut, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		absOut = opts.OutputDir
	}
	result := CreateResult{
		Template:     tmpl.Metadata.Name,
		Owner:        src.Owner,
		Repo:         src.Repo,
		Version:      src.Version,
		OutputDir:    absOut,
		Values:       values,
		Dependencies: deps,
	}
	if err := writeOutputJSON(opts.OutputJSON, opts.Stdout, result); err != nil {
		return fmt.Errorf("write --output-json: %w", err)
	}
	return nil
}

func writeOutputJSON(path string, stdout io.Writer, r CreateResult) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ensureOutputDir(dir string, force bool) error {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("--output %s exists and is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("--output %s is not empty (use --force to overwrite)", dir)
	}
	return nil
}

// ValidateTemplateName reports whether name is safe to use as a template
// directory — the exported half of validateTemplateName for callers that
// resolve a library themselves (the dashboard's template endpoints) and need
// the same refusal before joining user input into a path.
func ValidateTemplateName(name string) error {
	return validateTemplateName(name)
}

// validateTemplateName rejects empty names and anything that could escape the
// library checkout root via filepath.Join (separators, parent refs, hidden
// directories). The template argument is user input that we turn directly into
// a path segment, so it has to be sanitized.
func validateTemplateName(name string) error {
	if name == "" {
		return errors.New("template name is required")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid template name %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid template name %q (must be a single path segment)", name)
	}
	return nil
}

func selectPrompter(opts *CreateOptions) Prompter {
	return AutoPrompter(opts.Stdin, opts.Stderr, opts.NoInput)
}

// AutoPrompter returns a StdinPrompter when interactive prompting is viable:
// noInput is false and stdin is a real terminal. In CI / piped contexts it
// returns nil, so Resolve reports a clean "missing required parameter(s)"
// error instead of hanging on a read.
func AutoPrompter(stdin io.Reader, out io.Writer, noInput bool) Prompter {
	if noInput {
		return nil
	}
	f, ok := stdin.(*os.File)
	if !ok || !isTerminal(f.Fd()) {
		return nil
	}
	return NewStdinPrompter(stdin, out)
}

// ErrNoMessageParameters reports --subscribe or --publishes against a
// template whose manifest declares no message parameters. Callers map it
// to a usage error: the fault is in the requested combination, not the
// environment.
var ErrNoMessageParameters = errors.New("declares no message parameters")

// ErrSubscribeMessageConflict reports a message parameter value that does
// not match the scalar --subscribe message.
var ErrSubscribeMessageConflict = errors.New("message parameter conflicts with --subscribe")

// ErrMessageParameterConflict is the direction-neutral form of
// ErrSubscribeMessageConflict, reported when --publishes names a different
// message than a pre-set parameter value.
var ErrMessageParameterConflict = errors.New("message parameter conflicts with --publishes")

// ErrMessageDirection reports a message flag pointed at a template whose
// block kind wires messages the other way, or not at all. Callers map it
// to a usage error alongside ErrNoMessageParameters.
var ErrMessageDirection = errors.New("template does not wire messages in this direction")

// ErrMessageFlagsExclusive reports both message flags on one create — a
// programming-visible state (the CLI rejects the combination as a usage
// error before Create runs) guarded here so library callers cannot reach
// a record carrying both blocks.
var ErrMessageFlagsExclusive = errors.New("subscribe and publishes are mutually exclusive; one component is one direction")

// gateMessageDirection checks the template's block kind against the
// direction an explicit message flag would wire. Known-kinds mismatch and
// no-messaging kinds are hard errors — a flag that silently vanished
// would leave a record whose wiring contradicts the rendered code. An
// absent or unknown kind stays direction-neutral: today's behavior for
// --subscribe, and the tolerated shape for kind-less message templates.
func gateMessageDirection(tmpl *Template, flagDirection string) error {
	kind := blockKindFromLabels(tmpl.Metadata.Labels)
	dir := MessageDirection(kind)
	switch {
	case dir == flagDirection:
		return nil
	case dir == MessageDirectionNone && kind == BlockKindTransactional:
		return fmt.Errorf("template %q: %w\ntransactional integrations talk with external systems directly and wire no messages", tmpl.Metadata.Name, ErrMessageDirection)
	case dir == MessageDirectionNone:
		return nil // unknown or absent kind: direction-neutral
	case dir == MessageDirectionPublish:
		return fmt.Errorf("template %q: %w\n--subscribe wires a consuming component, but this template publishes messages; use --publishes", tmpl.Metadata.Name, ErrMessageDirection)
	default:
		return fmt.Errorf("template %q: %w\n--publishes wires a producing component, but this template subscribes to messages; use --subscribe", tmpl.Metadata.Name, ErrMessageDirection)
	}
}

// seedMessageParameters seeds every message parameter with the flag's
// message and rejects a pre-set value naming a different message. A
// present but non-string value conflicts like a wrong string would: the
// rendered parameter and the block snapshot must never be allowed to
// diverge silently.
//
// A template declaring more than one message parameter is refused under
// the flags: one create wires one message, so which parameter should take
// it is a question the flag cannot answer. The label accepts a comma list
// for template-side rendering; the multi-message wiring is the shape a
// list-shaped flag grows into.
//
// A list-shaped message parameter (a loader's routes) is seeded with one
// route for the message; a pre-set list must already route it.
func seedMessageParameters(tmpl *Template, params []string, sets map[string]any, flag string, conflictErr error, message string) error {
	templateName := tmpl.Metadata.Name
	if len(params) > 1 {
		return fmt.Errorf("template %q: %s wires one message, but the manifest declares %d message parameters (%s)", templateName, flag, len(params), strings.Join(params, ", "))
	}
	fields := indexFields(tmpl.Fields())
	for _, name := range params {
		value, present := sets[name]
		if fields[name].Type == "array" {
			list, _ := value.([]any)
			if text, ok := value.(string); ok {
				list, _ = coerce(text, "array").([]any)
			}
			if !present || isEmpty(value) {
				sets[name] = []any{map[string]any{KeyMessage: message}}
				continue
			}
			if !routesMessage(list, message) {
				return fmt.Errorf("template %q: %w\n%s resolved %q, but %s does not route it; remove the conflicting value or add the message", templateName, conflictErr, flag, message, name)
			}
			continue
		}
		if !present || isEmpty(value) {
			sets[name] = message
			continue
		}
		if s, ok := value.(string); !ok || s != message {
			return fmt.Errorf("template %q: %w\n%s resolved %q, but %s was set to %q; remove the conflicting value or make them match", templateName, conflictErr, flag, message, name, value)
		}
	}
	return nil
}
