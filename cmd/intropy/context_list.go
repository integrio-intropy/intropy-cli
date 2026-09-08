package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/spf13/cobra"
)

var contextListOpts struct {
	output string
}

// contextListJSON is the machine-readable document for 'context list'.
type contextListJSON struct {
	Contexts []string `json:"contexts"`
	Active   string   `json:"active,omitempty"`
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List customer contexts",
	Long:  "List the customer contexts in the user configuration file, marking the active one. Use --output json for a machine-readable document.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(contextListOpts.output, "plain", "json"); err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		// Sorted, never file or map order: the output is a stable contract
		// for both readers and tests.
		sort.Strings(names)
		if contextListOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(contextListJSON{Contexts: names, Active: cfg.CurrentContext})
		}
		if len(names) == 0 {
			stderr := cmd.ErrOrStderr()
			fmt.Fprintln(stderr, "no contexts configured")
			path, err := config.Path()
			if err != nil {
				path = "your config.yaml"
			}
			fmt.Fprintf(stderr, "add contexts to %s to get started\n", path)
			return nil
		}
		for _, name := range names {
			if name == cfg.CurrentContext {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (active)\n", name)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
		}
		return nil
	},
}

func init() {
	f := contextListCmd.Flags()
	f.StringVarP(&contextListOpts.output, "output", "o", "plain", flagUsageOutput)
	contextCmd.AddCommand(contextListCmd)
}
