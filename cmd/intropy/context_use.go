package main

import (
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/spf13/cobra"
)

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active customer context",
	Long:  "Switch the active customer context by writing currentContext to the user configuration file. The name must be one of the contexts defined in the file; 'intropy context list' shows them.",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		path, err := config.Path()
		if err != nil {
			return err
		}
		if _, ok := cfg.Contexts[name]; !ok {
			return newUsageErrorf("%v", config.UnknownContextError(path, name, cfg.Contexts))
		}
		if err := config.SetCurrentContext(path, name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "switched to context %s\n", name)
		return nil
	},
}

func init() {
	contextCmd.AddCommand(contextUseCmd)
}
