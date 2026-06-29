package blueprint

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
	fields := t.Fields()
	byName := indexFields(fields)
	out := map[string]any{}

	applyDefaultValues(fields, out)
	if err := applyValuesFiles(files, stdin, out, byName); err != nil {
		return nil, err
	}
	applySetValues(sets, out, byName)
	if err := promptForMissingRequired(fields, out, prompter); err != nil {
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

func promptForMissingRequired(fields []FieldSpec, values map[string]any, prompter Prompter) error {
	missing := missingRequired(fields, values)
	if len(missing) == 0 {
		return nil
	}
	if prompter == nil {
		names := make([]string, 0, len(missing))
		for _, f := range missing {
			names = append(names, f.Name)
		}
		return fmt.Errorf("missing required parameter(s): %s", strings.Join(names, ", "))
	}
	for _, f := range missing {
		ans, err := prompter.Prompt(f)
		if err != nil {
			return err
		}
		if isEmpty(ans) {
			return fmt.Errorf("required parameter %q has no value", f.Name)
		}
		values[f.Name] = ans
	}
	return nil
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

// renderExpr renders a Go text/template string (with sprig) against the
// supplied data map. Used for spec.values entries.
func renderExpr(expr string, data map[string]any) (string, error) {
	tmpl, err := template.New("expr").
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(expr)
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
