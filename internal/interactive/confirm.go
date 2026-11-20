package interactive

import (
	"context"
	"io"
	"os"

	"charm.land/huh/v2"
	"golang.org/x/term"
)

// ConfirmRequest describes one yes/no question.
type ConfirmRequest struct {
	Title       string
	Description string
}

// Confirmer asks a yes/no question. The answer defaults to no.
type Confirmer interface {
	Confirm(context.Context, ConfirmRequest) (bool, error)
}

// HuhConfirmer asks with Huh and keeps its terminal UI off stdout.
type HuhConfirmer struct {
	input  io.Reader
	output io.Writer
}

// NewTerminalConfirmer returns a Huh confirmer when both input and output are
// terminals. A nil result tells the caller to use its non-interactive path.
func NewTerminalConfirmer(input io.Reader, output io.Writer) Confirmer {
	in, inputIsFile := input.(*os.File)
	out, outputIsFile := output.(*os.File)
	if !inputIsFile || !outputIsFile || !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil
	}
	return &HuhConfirmer{input: input, output: output}
}

// Confirm presents one Huh confirm field and returns the answer.
func (c *HuhConfirmer) Confirm(ctx context.Context, req ConfirmRequest) (bool, error) {
	var yes bool
	field := huh.NewConfirm().
		Title(req.Title).
		Description(req.Description).
		Affirmative("Yes").
		Negative("No").
		Value(&yes)
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(c.input).
		WithOutput(c.output).
		WithAccessible(os.Getenv("ACCESSIBLE") != "")
	if err := form.RunWithContext(ctx); err != nil {
		return false, err
	}
	return yes, nil
}
