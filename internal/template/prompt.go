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

type Prompter interface {
	Prompt(field FieldSpec) (any, error)
}

// StdinPrompter is a line-based prompter. Enums render as a numbered list;
// every other type flows through one read-coerce-validate-retry loop.
// Invalid input re-prompts; EOF aborts.
type StdinPrompter struct {
	in      io.Reader
	out     io.Writer
	scanner *bufio.Scanner
}

func NewStdinPrompter(in io.Reader, out io.Writer) *StdinPrompter {
	return &StdinPrompter{in: in, out: out, scanner: bufio.NewScanner(in)}
}

func (p *StdinPrompter) Prompt(f FieldSpec) (any, error) {
	if len(f.Enum) > 0 {
		return p.promptEnum(f)
	}
	return p.promptScalar(f)
}

func (p *StdinPrompter) promptScalar(f FieldSpec) (any, error) {
	var re *regexp.Regexp
	if f.Pattern != "" {
		var err error
		re, err = regexp.Compile(f.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern for %q: %w", f.Name, err)
		}
	}
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
		if isTypedField(f.Type) {
			if _, stillStr := v.(string); stillStr {
				fmt.Fprintf(p.out, "  ! %q is not a valid %s\n", raw, f.Type)
				continue
			}
		}
		if re != nil {
			s, ok := v.(string)
			if !ok || !re.MatchString(s) {
				fmt.Fprintf(p.out, "  ! value must match %s\n", f.Pattern)
				continue
			}
		}
		return v, nil
	}
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
