package main

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/spf13/cobra"
)

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := skill.FindProject(".")
		if err != nil {
			if errors.Is(err, skill.ErrProjectNotFound) {
				cmd.Println("No skills installed.")
				return nil
			}
			return fmt.Errorf("list: %w", err)
		}

		lockfile, err := project.LoadLockfile()
		if err != nil {
			return fmt.Errorf("list: load lockfile: %w", err)
		}

		if len(lockfile.Skills) == 0 {
			cmd.Println("No skills installed.")
			cmd.Println("Use `intropy skills add <ref>` to add one.")
			return nil
		}

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tVERSION\tDIGEST\tPATH")
		for _, s := range lockfile.Skills {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				s.Name,
				s.Source.Tag,
				shortDigest(s.Source.Digest),
				s.Path,
			)
		}
		return tw.Flush()
	},
}

// shortDigest abbreviates "sha256:abc123..." to "sha256:abc123" (12 chars after prefix).
// Full digests are 71 chars and dominate the table; the prefix is enough to identify.
func shortDigest(d string) string {
	const prefix = "sha256:"
	if len(d) <= len(prefix)+12 {
		return d
	}
	return d[:len(prefix)+12]
}

func init() {
	skillsCmd.AddCommand(skillsListCmd)
}
