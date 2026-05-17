package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

type skillsUpdateFlags struct {
	all    bool
	output string
}

var skillsUpdateOpts skillsUpdateFlags

var skillsUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Update an installed skill to the version pinned by its collection",
	Long: `Reconciles an installed skill against the ref currently pinned by the
registered collections (as seen in the local index cache). If the resolved
ref differs from what's in skills.json, the new content is pulled and
.agents/skills/<name> is replaced.

Run 'intropy skills collection update <alias>' first to refresh the cache if
the upstream collection has been republished.

Pass --all to reconcile every installed skill at once.`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstalledSkills,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(skillsUpdateOpts.output, "json", "plain"); err != nil {
			return err
		}
		if len(args) == 0 && !skillsUpdateOpts.all {
			return newUsageErrorf("requires a skill name or --all")
		}
		if len(args) == 1 && skillsUpdateOpts.all {
			return newUsageErrorf("pass either a skill name or --all, not both")
		}

		project, err := skill.FindProject(".")
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}

		client, err := oci.NewClient()
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}

		installer := skill.NewInstaller(client, skill.NewTarGzExtractor(), project)
		updater := skill.NewUpdater(client, installer, project)

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		var names []string
		if skillsUpdateOpts.all {
			manifest, err := project.LoadManifest()
			if err != nil {
				return fmt.Errorf("update: %w", err)
			}
			if len(manifest.Skills) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No skills installed.")
				return nil
			}
			for _, e := range manifest.Skills {
				names = append(names, e.Name)
			}
		} else {
			names = []string{args[0]}
		}

		var results []skill.UpdateResult
		for _, name := range names {
			result, err := updater.Update(ctx, name)
			if err != nil {
				return fmt.Errorf("update: %w", err)
			}
			results = append(results, result)
			if skillsUpdateOpts.output == "plain" {
				if result.Changed {
					fmt.Fprintf(cmd.ErrOrStderr(), "Updated %s: %s -> %s\n", name, result.OldVersion, result.NewVersion)
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s already at %s\n", name, result.OldVersion)
				}
			}
		}

		if skillsUpdateOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}

		changed := 0
		for _, r := range results {
			if r.Changed {
				changed++
			}
		}
		if changed == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Nothing to update.")
		}

		return nil
	},
}

func init() {
	skillsUpdateCmd.Flags().BoolVar(
		&skillsUpdateOpts.all, "all", false,
		"Update every installed skill",
	)
	skillsUpdateCmd.Flags().StringVarP(&skillsUpdateOpts.output, "output", "o", "plain", "output format: plain or json")
	skillsCmd.AddCommand(skillsUpdateCmd)
}
