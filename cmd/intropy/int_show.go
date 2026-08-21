package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

// int show prints the scaffold record of one integration on disk. Locating
// and parsing the record live in internal/template; this file is flag
// plumbing and output formatting.
type intShowFlags struct {
	output string
}

var intShowOpts intShowFlags

var intShowCmd = &cobra.Command{
	Use:   "show [dir]",
	Short: "Show the integration scaffolded at a directory",
	Long: "Show the scaffold record (.intropy/scaffold.json) of the integration at dir (default: the current " +
		"directory, searched upward). The record pins the template, the release it was rendered from, and the " +
		"resolved parameter values. Use --output json to print the record unchanged.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(intShowOpts.output, "json", "plain"); err != nil {
			return err
		}
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		s, root, err := template.FindScaffold(dir)
		if err != nil {
			if errors.Is(err, template.ErrScaffoldNotFound) {
				return fmt.Errorf("show: no integration found at or above %s\nrun from a scaffolded integration directory, or use 'intropy int list' to find one", dir)
			}
			return fmt.Errorf("show: %w", err)
		}

		if intShowOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		}
		formatScaffold(cmd.OutOrStdout(), root, s)
		return nil
	},
}

// formatScaffold writes a human-readable summary of a scaffold record to w.
// The machine-readable form is the JSON-marshaled template.Scaffold itself.
func formatScaffold(w io.Writer, root string, s *template.Scaffold) {
	fmt.Fprintf(w, "%s\nscaffolded from %s @ %s/%s@%s\n", root, s.Template, s.Owner, s.Repo, s.Version)
	if s.Role != "" {
		fmt.Fprintf(w, "\nRole: %s\n", s.Role)
	}
	if s.BlockKind != "" {
		flow := s.DataFlow
		if flow == "" {
			flow = "unspecified"
		}
		fmt.Fprintf(w, "Block: %s (data flow: %s)\n", s.BlockKind, flow)
	}
	if len(s.DependsOn) > 0 {
		fmt.Fprintln(w, "\nDepends on:")
		for _, d := range s.DependsOn {
			fmt.Fprintf(w, "  %s -> %s\n", d.Template, d.Dir)
		}
	}
	if len(s.Values) > 0 {
		fmt.Fprintln(w, "\nValues:")
		keys := make([]string, 0, len(s.Values))
		for k := range s.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s: %v\n", k, s.Values[k])
		}
	}
}

func init() {
	f := intShowCmd.Flags()
	f.StringVarP(&intShowOpts.output, "output", "o", "plain", flagUsageOutput)
	intCmd.AddCommand(intShowCmd)
}
