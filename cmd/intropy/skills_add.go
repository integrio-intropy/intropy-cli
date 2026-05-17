package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

type skillsAddFlags struct {
	additionalBasePaths []string
	name                string
	collection          string
}

var skillsAddOpts skillsAddFlags

var skillsAddCmd = &cobra.Command{
	Use:   "add [ref]",
	Short: "Add a skill to the project and install it",
	Long: `Adds a skill to skills.json, pulls it from the OCI registry, and
extracts it under .agents/skills/<name>/.

Either pass a full OCI ref as a positional argument (e.g.
ghcr.io/example/skills/pr-review:1.0.0) or pass --name <skill-name> to
resolve the ref from a registered collection. Use --collection to
disambiguate when the same name appears in multiple collections.

If no skills.json exists in the current directory or any parent, an empty one
is created in the current directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && skillsAddOpts.name == "" {
			return newUsageErrorf("requires either a ref argument or --name")
		}
		if len(args) == 1 && skillsAddOpts.name != "" {
			return newUsageErrorf("pass either a ref argument or --name, not both")
		}
		if len(args) == 0 && skillsAddOpts.collection != "" && skillsAddOpts.name == "" {
			return newUsageErrorf("--collection only applies with --name")
		}

		project, err := resolveOrBootstrapProject(".")
		if err != nil {
			return fmt.Errorf("add: %w", err)
		}

		ref := ""
		if len(args) == 1 {
			ref = args[0]
		} else {
			resolution, err := skill.ResolveSkillName(project, skillsAddOpts.name, skillsAddOpts.collection)
			if err != nil {
				return fmt.Errorf("add: %w", err)
			}
			ref = resolution.Entry.Ref
			if ref == "" {
				return fmt.Errorf("add: collection entry for %q has no ref annotation", skillsAddOpts.name)
			}
		}

		client, err := oci.NewClient()
		if err != nil {
			return fmt.Errorf("add: %w", err)
		}

		installer := skill.NewInstaller(client, skill.NewTarGzExtractor(), project)
		adder := skill.NewAdder(client, installer, project)

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		entry, err := adder.Add(ctx, ref, skill.AddOptions{
			AdditionalBasePaths: skillsAddOpts.additionalBasePaths,
		})
		if err != nil {
			return fmt.Errorf("add: %w", err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Added %s @ %s\n", entry.Name, entry.Source.Tag)
		fmt.Fprintf(cmd.ErrOrStderr(), "  digest: %s\n", entry.Source.Digest)
		fmt.Fprintf(cmd.ErrOrStderr(), "  path:   %s\n", entry.Path)
		return nil
	},
}

// resolveOrBootstrapProject locates a skills.json by walking up from startDir;
// if none is found, it creates an empty one in startDir and returns the
// resulting Project. This inlines the only piece of the unported `init`
// command that `add` needs.
func resolveOrBootstrapProject(startDir string) (*skill.Project, error) {
	project, err := skill.FindProject(startDir)
	if err == nil {
		return project, nil
	}
	if !errors.Is(err, skill.ErrProjectNotFound) {
		return nil, err
	}

	abs, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}
	p := &skill.Project{Root: abs}
	if err := p.SaveManifest(&skill.Manifest{Skills: []skill.ManifestEntry{}}); err != nil {
		return nil, fmt.Errorf("bootstrap skills.json: %w", err)
	}
	return p, nil
}

func init() {
	skillsAddCmd.Flags().StringSliceVar(
		&skillsAddOpts.additionalBasePaths,
		"also-install-to", nil,
		"Additional directories to install the skill into (can be repeated)",
	)
	skillsAddCmd.Flags().StringVar(
		&skillsAddOpts.name,
		"name", "",
		"Install a skill by name from a registered collection",
	)
	skillsAddCmd.Flags().StringVar(
		&skillsAddOpts.collection,
		"collection", "",
		"Restrict --name lookup to a single registered collection",
	)
	_ = skillsAddCmd.RegisterFlagCompletionFunc("collection", completeRegisteredCollections)
	skillsCmd.AddCommand(skillsAddCmd)
}
