package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/spf13/cobra"
)

type skillsListFlags struct {
	output string
}

var skillsListOpts skillsListFlags

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(skillsListOpts.output, "json", "plain"); err != nil {
			return err
		}

		project, err := skill.FindProject(".")
		if err != nil {
			if errors.Is(err, skill.ErrProjectNotFound) {
				fmt.Fprintln(cmd.ErrOrStderr(), "No skills installed.")
				return nil
			}
			return fmt.Errorf("list: %w", err)
		}

		lockfile, err := project.LoadLockfile()
		if err != nil {
			return fmt.Errorf("list: load lockfile: %w", err)
		}

		if len(lockfile.Skills) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "No skills installed.")
			fmt.Fprintln(cmd.ErrOrStderr(), "Use `intropy skills add <ref>` to add one.")
			return nil
		}

		if skillsListOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(lockfile.Skills)
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
	f := skillsListCmd.Flags()
	f.StringVarP(&skillsListOpts.output, "output", "o", "plain", "output format: plain or json")
	skillsCmd.AddCommand(skillsListCmd)
}
