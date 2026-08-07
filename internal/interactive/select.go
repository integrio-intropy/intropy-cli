// Package interactive provides terminal interaction behind small interfaces so
// commands can keep non-interactive behavior explicit and testable.
package interactive

import (
	"context"
	"io"
	"os"

	"charm.land/huh/v2"
	"golang.org/x/term"
)

// SelectOption is one value shown by a Selector.
type SelectOption struct {
	Label string
	Value string
}

// SelectRequest describes one terminal selection.
type SelectRequest struct {
	Title       string
	Description string
	Options     []SelectOption
}

// Selector chooses one value from a closed list.
type Selector interface {
	Select(context.Context, SelectRequest) (string, error)
}

// HuhSelector presents selections with Huh and keeps its terminal UI off
// stdout, which remains available for command results.
type HuhSelector struct {
	input  io.Reader
	output io.Writer
}

// NewTerminalSelector returns a Huh selector when both input and output are
// terminals. A nil result tells the caller to use its non-interactive path.
func NewTerminalSelector(input io.Reader, output io.Writer) Selector {
	in, inputIsFile := input.(*os.File)
	out, outputIsFile := output.(*os.File)
	if !inputIsFile || !outputIsFile || !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil
	}
	return &HuhSelector{input: input, output: output}
}

// Select presents one Huh select field and returns its selected value.
func (s *HuhSelector) Select(ctx context.Context, req SelectRequest) (string, error) {
	options := make([]huh.Option[string], 0, len(req.Options))
	for _, option := range req.Options {
		options = append(options, huh.NewOption(option.Label, option.Value))
	}

	var selected string
	field := huh.NewSelect[string]().
		Title(req.Title).
		Description(req.Description).
		Options(options...).
		Value(&selected)
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(s.input).
		WithOutput(s.output).
		WithAccessible(os.Getenv("ACCESSIBLE") != "")
	if err := form.RunWithContext(ctx); err != nil {
		return "", err
	}
	return selected, nil
}
