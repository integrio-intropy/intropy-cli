package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/blueprint"
	"github.com/spf13/cobra"
)

type describeFlags struct {
	version string
	output  string
}

var intDescribeFlags describeFlags

var intDescribeCmd = &cobra.Command{
	Use:   "describe <template>",
	Short: "Describe an Intropy template",
	Long: "Print the template manifest — metadata and parameter schema — for the requested release. " +
		"Use --output json to emit a stable, machine-readable document (the same schema Backstage's frontend renders).",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBlueprints,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(intDescribeFlags.output, "json", "plain"); err != nil {
			return err
		}
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
		if intDescribeFlags.output == "json" {
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
	f.StringVar(&intDescribeFlags.version, "version", "", "template release tag (default: latest)")
	f.StringVarP(&intDescribeFlags.output, "output", "o", "plain", "output format: plain or json")
	intCmd.AddCommand(intDescribeCmd)
}
