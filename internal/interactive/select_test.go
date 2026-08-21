package interactive

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNewTerminalSelectorRejectsNonTerminalStreams(t *testing.T) {
	if got := NewTerminalSelector(&bytes.Buffer{}, &bytes.Buffer{}); got != nil {
		t.Fatalf("selector = %T, want nil for non-terminal streams", got)
	}
}

func TestHuhSelectorUsesTheRequestedOptions(t *testing.T) {
	t.Setenv("ACCESSIBLE", "1")
	var output bytes.Buffer
	selector := &HuhSelector{input: strings.NewReader("2\n"), output: &output}

	got, err := selector.Select(context.Background(), SelectRequest{
		Title:       "local binding for fno",
		Description: "external system fno",
		Options: []SelectOption{
			{Label: "sftp — SFTP server", Value: "sftp"},
			{Label: "http — HTTP stub", Value: "http"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "http" {
		t.Errorf("selection = %q, want http", got)
	}
	for _, want := range []string{"local binding for fno", "sftp", "http"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("accessible output does not contain %q:\n%s", want, output.String())
		}
	}
}
