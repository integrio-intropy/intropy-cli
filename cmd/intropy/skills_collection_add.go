package main

import (
	"context"
	"fmt"
	"time"

	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

type skillsCollectionAddFlags struct {
	name string
	ref  string
}

var skillsCollectionAddOpts skillsCollectionAddFlags

var skillsCollectionAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a collection to this project",
	Long: `Adds a collection registration to skills.json and caches the
collection's index for offline name lookups. The collection must already be
published to an OCI registry. After registration, skills can be installed by
name via 'intropy skills add --name <skill-name>'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if skillsCollectionAddOpts.name == "" {
			return fmt.Errorf("collection add: --name is required")
		}
		if skillsCollectionAddOpts.ref == "" {
			return fmt.Errorf("collection add: --ref is required")
		}

		if _, err := oci.ParseReference(skillsCollectionAddOpts.ref); err != nil {
			return fmt.Errorf("collection add: invalid ref %q: %w", skillsCollectionAddOpts.ref, err)
		}

		project, err := resolveOrBootstrapProject(".")
		if err != nil {
			return fmt.Errorf("collection add: %w", err)
		}

		manifest, err := project.LoadManifest()
		if err != nil {
			return fmt.Errorf("collection add: load manifest: %w", err)
		}

		for _, c := range manifest.Collections {
			if c.Name == skillsCollectionAddOpts.name {
				return fmt.Errorf("collection add: %q is already registered (ref: %s)",
					skillsCollectionAddOpts.name, c.Ref)
			}
		}

		client, err := oci.NewClient()
		if err != nil {
			return fmt.Errorf("collection add: %w", err)
		}

		// Fetch and cache the index up-front. If the registry rejects the
		// ref we'd rather fail before mutating skills.json.
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		index, err := client.PullIndex(ctx, skillsCollectionAddOpts.ref)
		if err != nil {
			return fmt.Errorf("collection add: fetch index: %w", err)
		}
		cached := &skill.CachedCollection{
			Ref:       skillsCollectionAddOpts.ref,
			FetchedAt: time.Now().UTC(),
			Index:     index,
		}
		if err := project.SaveCollectionCache(skillsCollectionAddOpts.name, cached); err != nil {
			return fmt.Errorf("collection add: cache index: %w", err)
		}

		manifest.Collections = append(manifest.Collections, skill.ManifestCollection{
			Name: skillsCollectionAddOpts.name,
			Ref:  skillsCollectionAddOpts.ref,
		})

		if err := project.SaveManifest(manifest); err != nil {
			return fmt.Errorf("collection add: %w", err)
		}
		cmd.Printf("Registered collection %q -> %s\n", skillsCollectionAddOpts.name, skillsCollectionAddOpts.ref)
		cmd.Printf("  cached %d skill(s)\n", len(index.Manifests))
		return nil
	},
}

func init() {
	skillsCollectionAddCmd.Flags().StringVar(
		&skillsCollectionAddOpts.name, "name", "",
		"Local name for the collection",
	)
	skillsCollectionAddCmd.Flags().StringVar(
		&skillsCollectionAddOpts.ref, "ref", "",
		"OCI reference of the collection",
	)
	skillsCollectionCmd.AddCommand(skillsCollectionAddCmd)
}
