package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/intropy/intropy-cli/internal/blueprint"
	"github.com/spf13/cobra"
)

type describeFlags struct {
	version string
	json    bool
}

var intDescribeFlags describeFlags

var intDescribeCmd = &cobra.Command{
	Use:   "describe <blueprint>",
	Short: "Describe an Intropy blueprint",
	Long: "Print the blueprint manifest — metadata and parameter schema — for the requested release. " +
		"Use --json to emit a stable, machine-readable document (the same schema Backstage's frontend renders).",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBlueprints,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		result, err := blueprint.Describe(ctx, blueprint.DescribeOptions{
			Blueprint: args[0],
			Version:   intDescribeFlags.version,
			UserAgent: "intropy-cli/" + version,
		})
		if err != nil {
			return err
		}
		if intDescribeFlags.json {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		result.FormatText(cmd.OutOrStdout())
		return nil
	},
}

func init() {
	f := intDescribeCmd.Flags()
	f.StringVar(&intDescribeFlags.version, "version", "", "blueprint release tag (default: latest)")
	f.BoolVar(&intDescribeFlags.json, "json", false, "emit machine-readable JSON")
	intCmd.AddCommand(intDescribeCmd)
}
