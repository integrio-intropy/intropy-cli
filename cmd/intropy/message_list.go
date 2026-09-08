package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/spf13/cobra"
)

type messageListFlags struct {
	output      string
	group       string
	registryURL string
}

var messageListOpts messageListFlags

var messageListCmd = &cobra.Command{
	Use:   "list [dir]",
	Short: "List messages from the registry and this workspace",
	Long: "List messages: every message the read-only xRegistry serves, merged with the messages this workspace's scaffold records declare through publishes blocks. " +
		"Reads " + template.ScaffoldRelPath + " under dir (default: the current directory) and one GET against the registry. " +
		"Use --group to narrow to one registry message group; workspace messages have no group and are hidden by the filter. " +
		"Use --output json for a machine-readable document including envelope metadata and producing channels.",
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(messageListOpts.output, "json", "plain"); err != nil {
			return err
		}
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("message list: %w", err)
		} else if !info.IsDir() {
			return newUsageErrorf("message list: %s is not a directory", dir)
		}

		stderr := cmd.ErrOrStderr()
		var entries []MessageEntry
		if messageListOpts.group == "" {
			entries = append(entries, workspaceMessages(dir, func(w error) {
				fmt.Fprintln(stderr, "warning:", w)
			})...)
		}
		client, err := registryClient(messageListOpts.registryURL)
		if err != nil {
			return err
		}
		registryEntries, err := registryMessages(cmd.Context(), client, messageListOpts.group)
		if err != nil {
			return err
		}
		entries = append(entries, registryEntries...)

		if messageListOpts.output == "json" {
			if entries == nil {
				entries = []MessageEntry{}
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		}

		if len(entries) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "no messages found")
			return nil
		}

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "MESSAGE\tSOURCE\tGROUP\tCHANNEL\tSCHEMA")
		for _, e := range entries {
			channel := strings.Join(e.Channels, ", ")
			if channel == "" {
				channel = "-"
			}
			schema := e.DataSchemaName
			if schema == "" {
				schema = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Message, e.Source, e.Group, channel, schema)
		}
		return tw.Flush()
	},
}

func init() {
	f := messageListCmd.Flags()
	f.StringVarP(&messageListOpts.output, "output", "o", "plain", flagUsageOutput)
	f.StringVar(&messageListOpts.group, "group", "", "only registry messages in this message group")
	f.StringVar(&messageListOpts.registryURL, "registry-url", "", flagUsageRegistryURL)
	messageCmd.AddCommand(messageListCmd)
}
