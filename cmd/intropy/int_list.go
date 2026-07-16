package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type intListFlags struct {
	output string
}

var intListOpts intListFlags

var intListCmd = &cobra.Command{
	Use:   "list [dir]",
	Short: "List scaffolded integrations under a directory",
	Long: "Walk the directory tree from dir (default: the current directory) and list every project carrying a " +
		template.ScaffoldRelPath + " record written by `int create`. " +
		"Matched projects are not descended into, and .git, .intropy, node_modules, bin and dist are skipped. " +
		"Use --output json for a machine-readable document including the pinned source and scaffold values.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(intListOpts.output, "json", "plain"); err != nil {
			return err
		}
		root := "."
		if len(args) == 1 {
			root = args[0]
		}
		if info, err := os.Stat(root); err != nil {
			return fmt.Errorf("list: %w", err)
		} else if !info.IsDir() {
			return newUsageErrorf("list: %s is not a directory", root)
		}

		entries, warnings := template.ListScaffolds(root)
		for _, w := range warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
		}

		if intListOpts.output == "json" {
			if entries == nil {
				entries = []template.ScaffoldEntry{}
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		}

		if len(entries) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "No scaffolded integrations found.")
			return nil
		}

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tTEMPLATE\tVERSION")
		for _, e := range entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Path, e.Template, e.Version)
		}
		return tw.Flush()
	},
}

func init() {
	f := intListCmd.Flags()
	f.StringVarP(&intListOpts.output, "output", "o", "plain", "output format: plain or json")
	intCmd.AddCommand(intListCmd)
}
