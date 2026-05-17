package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type skillsCollectionPublishFlags struct {
	force bool
}

var skillsCollectionPublishOpts skillsCollectionPublishFlags

var skillsCollectionPublishCmd = &cobra.Command{
	Use:   "publish <spec-file> <ref>",
	Short: "Publish a collection to an OCI registry",
	Long: `Reads a collection spec file (YAML), resolves each referenced skill
to its current digest, builds an OCI Image Index, and pushes it.

Example spec file:
  name: example-skills
  description: Curated example skills
  skills:
    - ref: ghcr.io/example/skills/pr-review:1.2.0
    - ref: ghcr.io/example/skills/upgrade-spring:2.1.0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		specPath := args[0]
		collectionRef := args[1]

		spec, err := loadCollectionSpec(specPath)
		if err != nil {
			return fmt.Errorf("collection publish: %w", err)
		}

		client, err := oci.NewClient()
		if err != nil {
			return fmt.Errorf("collection publish: %w", err)
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		publisher := skill.NewCollectionPublisher(client)
		desc, err := publisher.Publish(ctx, spec, collectionRef, skillsCollectionPublishOpts.force)
		if err != nil {
			return fmt.Errorf("collection publish: %w", err)
		}

		cmd.Printf("Published collection %s\n", collectionRef)
		cmd.Printf("  digest: %s\n", desc.Digest)
		cmd.Printf("  skills: %d\n", len(spec.Skills))
		return nil
	},
}

func loadCollectionSpec(path string) (*skill.CollectionSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	var spec skill.CollectionSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("spec: name is required")
	}
	if len(spec.Skills) == 0 {
		return nil, fmt.Errorf("spec: at least one skill is required")
	}
	return &spec, nil
}

func init() {
	skillsCollectionPublishCmd.Flags().BoolVar(&skillsCollectionPublishOpts.force, "force", false,
		"Overwrite the collection tag if it already exists")
	skillsCollectionCmd.AddCommand(skillsCollectionPublishCmd)
}
