package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// StdinValuesPath is the sentinel a caller may pass in the values-file list
// to signal "read a single values document from this reader". It mirrors the
// `-f -` idiom used by helm, kubectl, and curl.
const StdinValuesPath = "-"

// Resolve produces the final value map by layering, in order:
//
//	defaults declared in spec.parameters.properties[*].default
//	--values files (in supplied order; "-" reads one doc from stdin)
//	--set flags (string values, coerced to declared JSON Schema types)
//	prompts for required parameters still missing (when prompter != nil)
//
// stdin is consumed at most once across the files list; passing "-" twice is
// an error. The merged map is then validated against the JSON Schema in
// spec.parameters. Finally, every entry in spec.values is rendered as a Go
// text/template (with sprig) against the merged map and added under its key.
// A spec.values key that collides with a parameter name is rejected.
func Resolve(t *Template, files []string, stdin io.Reader, sets map[string]any, prompter Prompter) (map[string]any, error) {
	return ResolveWith(t, ResolveOptions{Files: files, Stdin: stdin, Sets: sets, Prompter: prompter})
}

// ResolveLayered is Resolve with an extra base layer that sits between the
// spec.parameters defaults and the --values files. Callers use it to seed
// values from an earlier resolution (e.g. a committed scaffold record) so
// they take effect without re-prompting but still yield to explicit
// --values / --set input.
func ResolveLayered(t *Template, base map[string]any, files []string, stdin io.Reader, sets map[string]any, prompter Prompter) (map[string]any, error) {
	return ResolveWith(t, ResolveOptions{Base: base, Files: files, Stdin: stdin, Sets: sets, Prompter: prompter})
}

// ResolveOptions is the full input to value resolution. The zero value is
// a bare resolve: schema defaults, then nothing else. Each field names one
// layer or instrument, in the order resolution applies them — Base under
// Files under Sets, Facts informing prefill and prompts, Prompter and
// Notes as the interactive and diagnostic surfaces.
type ResolveOptions struct {
	// Base sits between the schema defaults and the values files. Callers
	// seed it from an earlier resolution (e.g. a committed scaffold record)
	// so it takes effect without re-prompting but yields to explicit input.
	Base map[string]any

	// Facts is the workspace's declared wiring. It changes resolution in
	// two deliberate ways, both overridable with --set/--values:
	//
	//   - prefill: a required parameter with exactly one workspace
	//     candidate resolves to it without prompting, behaving like an
	//     optional parameter with a workspace-derived default. Each
	//     prefill is noted on Notes so the value never lands invisibly.
	//   - prompt suggestions: a parameter with several candidates prompts
	//     with them as a pick list; the prompter's confirmation is the
	//     value.
	//
	// A nil facts index resolves exactly as ResolveLayered always has.
	Facts *WorkspaceFacts

	// Files are --values files in supplied order; "-" reads one document
	// from Stdin (consumed at most once).
	Files []string
	Stdin io.Reader

	// Sets are --set flag values, applied last of the input layers.
	Sets map[string]any

	// Prompter resolves required parameters still missing after every
	// input layer; nil turns a miss into the missing-parameter error.
	Prompter Prompter

	// Notes receives one line per prefilled value — typically stderr. Nil
	// silences the notes, not the prefill.
	Notes io.Writer
}

// ResolveWith is the resolution all of Resolve, ResolveLayered, and the
// create flows share: the input layers in declaration order, prefill and
// prompts for what they leave open, then schema validation and derived
// values.
func ResolveWith(t *Template, opts ResolveOptions) (map[string]any, error) {
	fields := t.Fields()
	byName := indexFields(fields)
	out := map[string]any{}

	applyDefaultValues(fields, out)
	for k, v := range opts.Base {
		out[k] = coerceKnownFieldValue(k, v, byName)
	}
	if err := applyValuesFiles(opts.Files, opts.Stdin, out, byName); err != nil {
		return nil, err
	}
	applySetValues(opts.Sets, out, byName)
	prefill, _ := candidatesByArity(fields, out, opts.Facts)
	applyPrefill(fields, out, prefill, opts.Notes)
	if err := promptForMissingRequired(fields, out, opts.Facts, opts.Prompter); err != nil {
		return nil, err
	}
	if err := validateSchema(t.Spec.Parameters, out); err != nil {
		return nil, fmt.Errorf("parameter validation: %w", err)
	}
	if err := renderDerivedValues(t.Spec.Values, out, byName); err != nil {
		return nil, err
	}

	return out, nil
}

// candidatesByArity computes every open required parameter's workspace
// candidates once and splits them by what they imply: exactly one
// candidate is a prefill pick, several are prompt suggestions. Fields
// already valued — by default, base, --values, or --set — have neither.
// The single computation keeps prefill and prompts reading one snapshot,
// so the two paths can never disagree about what the workspace offers.
func candidatesByArity(fields []FieldSpec, values map[string]any, facts *WorkspaceFacts) (prefill, suggestions map[string][]string) {
	open := make([]FieldSpec, 0, len(fields))
	for _, f := range fields {
		if !f.Required {
			continue
		}
		if v, ok := values[f.Name]; ok && !isEmpty(v) {
			continue
		}
		open = append(open, f)
	}
	prefill = map[string][]string{}
	suggestions = map[string][]string{}
	for name, candidates := range Suggest(open, facts, values) {
		if len(candidates) == 1 {
			prefill[name] = candidates
		} else {
			suggestions[name] = candidates
		}
	}
	return prefill, suggestions
}

// applyPrefill resolves the single-candidate picks candidatesByArity
// separated, chaining as it goes: a prefilled topic constrains contract's
// candidates to that topic's contract, so a workspace with one topic
// resolves the whole wiring without a prompt. Each prefill is noted so
// the value never lands invisibly.
func applyPrefill(fields []FieldSpec, values map[string]any, prefill map[string][]string, notes io.Writer) {
	for _, f := range fields {
		candidates, ok := prefill[f.Name]
		if !ok {
			continue
		}
		if v, valued := values[f.Name]; valued && !isEmpty(v) {
			continue
		}
		values[f.Name] = candidates[0]
		if notes != nil {
			fmt.Fprintf(notes, "%s: %s (from workspace; override with --set %s=<value>)\n", f.Name, candidates[0], f.Name)
		}
	}
}

func applyDefaultValues(fields []FieldSpec, values map[string]any) {
	for _, f := range fields {
		if f.Default != nil {
			values[f.Name] = f.Default
		}
	}
}

func applyValuesFiles(files []string, stdin io.Reader, values map[string]any, byName map[string]FieldSpec) error {
	stdinUsed := false
	for _, path := range files {
		if path == StdinValuesPath {
			if stdinUsed {
				return fmt.Errorf("--values - specified more than once (stdin can only be read once)")
			}
			if stdin == nil {
				return fmt.Errorf("--values - specified but no stdin reader provided")
			}
			data, err := io.ReadAll(stdin)
			if err != nil {
				return fmt.Errorf("read values from stdin: %w", err)
			}
			if err := mergeValuesBytes(data, "stdin", values, byName); err != nil {
				return err
			}
			stdinUsed = true
			continue
		}
		if err := mergeValuesFile(path, values, byName); err != nil {
			return err
		}
	}
	return nil
}

func applySetValues(sets map[string]any, values map[string]any, byName map[string]FieldSpec) {
	for k, v := range sets {
		values[k] = coerceKnownFieldValue(k, v, byName)
	}
}

func promptForMissingRequired(fields []FieldSpec, values map[string]any, facts *WorkspaceFacts, prompter Prompter) error {
	missing := missingRequired(fields, values)
	if len(missing) == 0 {
		return nil
	}
	suggestions := suggestMissing(missing, facts, values)
	if prompter == nil {
		return missingRequiredError(missing, suggestions)
	}
	for _, f := range missing {
		f.Suggestions = suggestions[f.Name]
		ans, _, err := prompter.Prompt(f)
		if err != nil {
			return err
		}
		if isEmpty(ans) {
			return fmt.Errorf("required parameter %q has no value", f.Name)
		}
		values[f.Name] = ans
		// Re-derive after every answer, picked or typed: the answered
		// parameter leaves the open set, and its value may constrain what
		// remains (a known topic narrows the contract candidates).
		// Deriving candidates is the prompt loop's invariant — never cache
		// a suggestion list across answers.
		suggestions = suggestMissing(missing, facts, values)
	}
	return nil
}

// suggestMissing derives candidates for the still-open fields from the
// current value map. It is the prompt loop's one derivation point —
// called before the first prompt and again after every answer — so a
// suggestion list never outlives the values it was derived from.
// Answered fields drop out of the open set; the map holds only what a
// prompt or the missing-parameter error can still act on.
func suggestMissing(missing []FieldSpec, facts *WorkspaceFacts, values map[string]any) map[string][]string {
	unresolved := make([]FieldSpec, 0, len(missing))
	for _, f := range missing {
		if v, ok := values[f.Name]; ok && !isEmpty(v) {
			continue
		}
		unresolved = append(unresolved, f)
	}
	return Suggest(unresolved, facts, values)
}

// missingRequiredError reports unmet required parameters, appending one
// hint line per field the workspace has candidates for. The hints name
// what --set would accept so a CI failure documents the values the run
// could not ask for.
func missingRequiredError(missing []FieldSpec, suggestions map[string][]string) error {
	names := make([]string, 0, len(missing))
	for _, f := range missing {
		names = append(names, f.Name)
	}
	err := fmt.Sprintf("missing required parameter(s): %s", strings.Join(names, ", "))
	for _, f := range missing {
		if c := suggestions[f.Name]; len(c) > 0 {
			err += fmt.Sprintf("\nknown %s values in this workspace: %s", f.Name, strings.Join(c, ", "))
		}
	}
	return fmt.Errorf("%s", err)
}

func renderDerivedValues(derived map[string]string, values map[string]any, byName map[string]FieldSpec) error {
	for k, expr := range derived {
		if _, collides := byName[k]; collides {
			return fmt.Errorf("spec.values.%s collides with a parameter of the same name", k)
		}
		rendered, err := renderExpr(expr, values)
		if err != nil {
			return fmt.Errorf("spec.values.%s: %w", k, err)
		}
		values[k] = rendered
	}
	return nil
}

func mergeValuesFile(path string, into map[string]any, byName map[string]FieldSpec) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read values file %s: %w", path, err)
	}
	return mergeValuesBytes(data, path, into, byName)
}

func mergeValuesBytes(data []byte, source string, into map[string]any, byName map[string]FieldSpec) error {
	var fileVals map[string]any
	if err := yaml.Unmarshal(data, &fileVals); err != nil {
		return fmt.Errorf("parse values from %s: %w", source, err)
	}
	for k, v := range fileVals {
		into[k] = coerceKnownFieldValue(k, v, byName)
	}
	return nil
}

func coerceKnownFieldValue(name string, value any, byName map[string]FieldSpec) any {
	field, ok := byName[name]
	if !ok {
		return value
	}
	str, ok := value.(string)
	if !ok {
		return value
	}
	return coerce(str, field.Type)
}

func indexFields(fields []FieldSpec) map[string]FieldSpec {
	m := make(map[string]FieldSpec, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

func missingRequired(fields []FieldSpec, values map[string]any) []FieldSpec {
	var missing []FieldSpec
	for _, f := range fields {
		if !f.Required {
			continue
		}
		if v, ok := values[f.Name]; ok && !isEmpty(v) {
			continue
		}
		missing = append(missing, f)
	}
	return missing
}

func validateSchema(schema map[string]any, values map[string]any) error {
	raw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("inline://params.json", bytes.NewReader(raw)); err != nil {
		return err
	}
	s, err := c.Compile("inline://params.json")
	if err != nil {
		return err
	}
	return s.Validate(values)
}

// compileExpr parses a Go text/template string with sprig helpers and
// missingkey=error, so an undefined value name is a loud failure rather than a
// silently empty string. Separate from renderExpr because spec.files rules are
// parsed at load time to reject a syntax error early, and only evaluated later
// once values exist.
func compileExpr(expr string) (*template.Template, error) {
	return template.New("expr").
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(expr)
}

// renderExpr renders a Go text/template string (with sprig) against the
// supplied data map. Used for spec.values entries and spec.files conditions.
func renderExpr(expr string, data map[string]any) (string, error) {
	tmpl, err := compileExpr(expr)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// ParseSets converts ["key=value", ...] into a map. Values stay as strings;
// Resolve coerces them to the JSON Schema type when the key matches a known
// parameter.
func ParseSets(items []string) (map[string]any, error) {
	out := map[string]any{}
	for _, item := range items {
		i := strings.Index(item, "=")
		if i <= 0 {
			return nil, fmt.Errorf("invalid --set %q (expected key=value)", item)
		}
		out[item[:i]] = item[i+1:]
	}
	return out, nil
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	default:
		return false
	}
}
