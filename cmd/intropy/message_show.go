package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type messageShowFlags struct {
	output      string
	registryURL string
}

var messageShowOpts messageShowFlags

var messageShowCmd = &cobra.Command{
	Use:   "show <ref> [dir]",
	Short: "Show one message's definition",
	Long: "Show one message's definition by reference — the bare message id or a /messagegroups/<gid>/messages/<mid> xid. " +
		"A workspace publish (a publishes block in a scaffold record under dir, default current directory) resolves locally; everything else reads the xRegistry export. " +
		"The definition covers the envelope metadata, the producing channels, and the schema pin (the immutable default-version URL a CloudEvent dataschema references). " +
		"Use --output json for the same document machine-readable.",
	Args: usageArgs(cobra.RangeArgs(1, 2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := args[0]
		dir := "."
		if len(args) == 2 {
			dir = args[1]
		}
		if err := validateOutputFlag(messageShowOpts.output, "json", "plain"); err != nil {
			return err
		}
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("message show: %w", err)
		} else if !info.IsDir() {
			return newUsageErrorf("message show: %s is not a directory", dir)
		}

		// Workspace first: a local publish answers without the network, and
		// a name both sides declare is displayed with its local source.
		var wsWarnings []error
		for _, e := range workspaceMessages(dir, func(w error) { wsWarnings = append(wsWarnings, w) }) {
			if e.Message == ref {
				for _, w := range wsWarnings {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
				}
				return printMessage(cmd, &e, messageShowOpts.output)
			}
		}
		for _, w := range wsWarnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
		}

		client, err := registryClient(messageShowOpts.registryURL)
		if err != nil {
			return err
		}
		e, err := registryMessage(cmd.Context(), client, ref)
		if err != nil {
			return err
		}
		return printMessage(cmd, e, messageShowOpts.output)
	},
}

// printMessage writes one message: human rows to stdout, or the JSON
// document. Diagnostics never share the stdout stream.
func printMessage(cmd *cobra.Command, e *MessageEntry, output string) error {
	if output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(e)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "message:   %s\n", e.Message)
	fmt.Fprintf(cmd.OutOrStdout(), "source:    %s\n", e.Source)
	if e.Type != "" && e.Type != e.Message {
		fmt.Fprintf(cmd.OutOrStdout(), "type:      %s\n", e.Type)
	}
	if e.Envelope != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "envelope:  %s\n", e.Envelope)
	}
	if e.Group != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "group:     %s\n", e.Group)
	}
	if e.Path != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "path:      %s\n", e.Path)
	}
	if e.Contract != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "contract:  %s\n", e.Contract)
	}
	if len(e.Channels) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "channels:  %s\n", strings.Join(e.Channels, ", "))
	}
	if e.DataSchema != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "dataschema: %s\n", e.DataSchema)
	}
	if e.DataSchemaURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "dataschemaurl: %s\n", e.DataSchemaURL)
	}
	for _, kv := range e.EnvelopeMeta {
		required := ""
		if kv.Required {
			required = " (required)"
		}
		value := "-"
		if kv.Value != "" {
			value = kv.Value
		}
		fmt.Fprintf(cmd.OutOrStdout(), "envelope %s: %s%s\n", kv.Name, value, required)
	}
	return nil
}

func init() {
	f := messageShowCmd.Flags()
	f.StringVarP(&messageShowOpts.output, "output", "o", "plain", flagUsageOutput)
	f.StringVar(&messageShowOpts.registryURL, "registry-url", "", flagUsageRegistryURL)
	messageCmd.AddCommand(messageShowCmd)
}
