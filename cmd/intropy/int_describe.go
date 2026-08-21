package main

import (
	"github.com/spf13/cobra"
)

// int describe used to print a template's manifest; that is `template show`
// now. The command remains as a hidden stub so a user following old docs or
// muscle memory gets a pointer instead of an unknown-command error. It
// always fails; remove the stub once the rename is old enough that nobody
// trips over it.
var intDescribeCmd = &cobra.Command{
	Use:    "describe <template>",
	Short:  "Describe an Intropy template",
	Args:   cobra.ArbitraryArgs,
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target := "template show"
		if len(args) > 0 {
			target += " " + args[0]
		}
		return newUsageErrorf("'int describe' has been replaced\nuse 'intropy %s' to inspect a template, or 'intropy int show' to inspect a scaffolded integration", target)
	},
}

func init() {
	intCmd.AddCommand(intDescribeCmd)
}
