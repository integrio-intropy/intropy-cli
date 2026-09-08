package template

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Prompter resolves one missing required field with the user. The
// applied result reports whether a field Suggestion produced the value
// (shown default accepted, or a list pick) as opposed to the user typing
// it fresh. Resolution re-derives candidates after every answer
// regardless; the flag exists for prompters and callers that distinguish
// a confirmation from a novel entry.
type Prompter interface {
	Prompt(field FieldSpec) (value any, applied bool, err error)
}

// StdinPrompter is a line-based prompter. Enums render as a numbered list;
// every other type flows through one read-coerce-validate-retry loop.
// Invalid input re-prompts; EOF aborts.
//
// Suggestions change the rendering, never the authority: one suggestion
// becomes the shown default (Enter accepts, typing overrides), several
// render as a numbered list that still accepts a fresh value. Only an
// accepted or picked suggestion is reported applied.
type StdinPrompter struct {
	in      io.Reader
	out     io.Writer
	scanner *bufio.Scanner
}

func NewStdinPrompter(in io.Reader, out io.Writer) *StdinPrompter {
	return &StdinPrompter{in: in, out: out, scanner: bufio.NewScanner(in)}
}

func (p *StdinPrompter) Prompt(f FieldSpec) (any, bool, error) {
	if len(f.Suggestions) > 0 && len(f.Enum) == 0 {
		return p.promptSuggested(f)
	}
	if len(f.Enum) > 0 {
		v, err := p.promptEnum(f)
		return v, false, err
	}
	v, err := p.promptScalar(f)
	return v, false, err
}

// promptSuggested handles a scalar field with workspace-derived
// candidates. A single candidate is the prompt default; several render as
// a pick list that stays open — a scaffold's whole point may be a topic
// nobody has declared yet, so the list never replaces free input.
func (p *StdinPrompter) promptSuggested(f FieldSpec) (any, bool, error) {
	if len(f.Suggestions) == 1 {
		v, err := p.promptScalar(withDefault(f, f.Suggestions[0]))
		if err != nil {
			return nil, false, err
		}
		if s, ok := v.(string); ok && s == f.Suggestions[0] {
			return v, true, nil
		}
		return v, false, nil
	}
	return p.promptSuggestedList(f)
}

// promptSuggestedList renders candidates as a numbered list plus a
// free-text escape, reusing the enum prompt's read-validate-retry loop
// with one difference: input that matches no candidate is a new value,
// not an error.
func (p *StdinPrompter) promptSuggestedList(f FieldSpec) (any, bool, error) {
	p.writeHeading(f)
	for i, s := range f.Suggestions {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, s)
	}
	fmt.Fprintln(p.out, "  or type a new value")
	for {
		fmt.Fprint(p.out, ": ")
		raw, ok, err := p.readLine()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, io.EOF
		}
		if raw == "" {
			fmt.Fprintln(p.out, "  ! please choose one")
			continue
		}
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= len(f.Suggestions) {
			return coerce(f.Suggestions[n-1], f.Type), true, nil
		}
		for _, s := range f.Suggestions {
			if raw == s {
				return coerce(s, f.Type), true, nil
			}
		}
		v := coerce(raw, f.Type)
		if err := p.validateScalar(f, v); err != nil {
			fmt.Fprintf(p.out, "  ! %s\n", err)
			continue
		}
		return v, false, nil
	}
}

func (p *StdinPrompter) promptScalar(f FieldSpec) (any, error) {
	for {
		p.writeLabel(f)
		raw, ok, err := p.readLine()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, io.EOF
		}
		if raw == "" {
			return f.Default, nil
		}
		v := coerce(raw, f.Type)
		if err := p.validateScalar(f, v); err != nil {
			fmt.Fprintf(p.out, "  ! %s\n", err)
			continue
		}
		return v, nil
	}
}

// validateScalar is the retry-condition half of promptScalar, shared with
// the free-text path of a suggestion list: typed fields must parse, and a
// declared pattern must match.
func (p *StdinPrompter) validateScalar(f FieldSpec, v any) error {
	if isTypedField(f.Type) {
		if _, stillStr := v.(string); stillStr {
			return fmt.Errorf("%v is not a valid %s", v, f.Type)
		}
	}
	if f.Pattern != "" {
		re, err := regexp.Compile(f.Pattern)
		if err != nil {
			return fmt.Errorf("invalid pattern for %q: %w", f.Name, err)
		}
		s, ok := v.(string)
		if !ok || !re.MatchString(s) {
			return fmt.Errorf("value must match %s", f.Pattern)
		}
	}
	return nil
}

func (p *StdinPrompter) promptEnum(f FieldSpec) (any, error) {
	p.writeHeading(f)
	options := make([]string, 0, len(f.Enum))
	for i, e := range f.Enum {
		s := fmt.Sprint(e)
		options = append(options, s)
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, s)
	}
	defStr, hasDef := f.Default.(string)
	for {
		if hasDef {
			fmt.Fprintf(p.out, "[%s]: ", defStr)
		} else {
			fmt.Fprint(p.out, ": ")
		}
		raw, ok, err := p.readLine()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, io.EOF
		}
		if raw == "" {
			if hasDef {
				return coerce(defStr, f.Type), nil
			}
			fmt.Fprintln(p.out, "  ! please choose one")
			continue
		}
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= len(options) {
			return coerce(options[n-1], f.Type), nil
		}
		for _, o := range options {
			if o == raw {
				return coerce(o, f.Type), nil
			}
		}
		fmt.Fprintf(p.out, "  ! %q is not one of the options\n", raw)
	}
}

func (p *StdinPrompter) writeLabel(f FieldSpec) {
	label := f.Name
	if f.Title != "" {
		label = f.Title
	}
	if f.Description != "" {
		label = fmt.Sprintf("%s (%s)", label, f.Description)
	}
	switch d := f.Default.(type) {
	case bool:
		if d {
			label += " [Y/n]"
		} else {
			label += " [y/N]"
		}
	case nil:
		if f.Type == "boolean" {
			label += " [y/n]"
		}
	default:
		label = fmt.Sprintf("%s [%v]", label, d)
	}
	fmt.Fprintf(p.out, "%s: ", label)
}

func (p *StdinPrompter) writeHeading(f FieldSpec) {
	title := f.Name
	if f.Title != "" {
		title = f.Title
	}
	if f.Description != "" {
		fmt.Fprintf(p.out, "%s (%s)\n", title, f.Description)
	} else {
		fmt.Fprintln(p.out, title)
	}
}

func (p *StdinPrompter) readLine() (string, bool, error) {
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	return strings.TrimSpace(p.scanner.Text()), true, nil
}

// withDefault returns a copy of the field whose shown default is the
// suggestion. The schema default already lost the bracket when suggestions
// were computed (Suggest drops candidates equal to it), so overwriting is
// safe for the prompt's lifetime.
func withDefault(f FieldSpec, suggestion string) FieldSpec {
	f.Default = suggestion
	return f
}

func isTypedField(typ string) bool {
	return typ == "boolean" || typ == "integer" || typ == "number"
}

// coerce parses a string into the type declared by a JSON Schema property.
// Returns the original string if parsing fails — JSON Schema validation
// downstream produces a clean type error in that case.
func coerce(s, typ string) any {
	switch typ {
	case "boolean":
		switch strings.ToLower(s) {
		case "y", "yes", "true", "1":
			return true
		case "n", "no", "false", "0":
			return false
		}
	case "integer":
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	case "number":
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	}
	return s
}

func isTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}
