package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/skill"
	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

// skills collection update refreshes (or, with --ref, re-points) the
// cached index of a registered collection. Cache mechanics live in
// internal/skill's collectioncache; this file is flag plumbing.
type skillsCollectionUpdateFlags struct {
	ref    string
	output string
}

var skillsCollectionUpdateOpts skillsCollectionUpdateFlags

var skillsCollectionUpdateCmd = &cobra.Command{
	Use:   "update <alias>",
	Short: "Refresh or bump the cached index for a registered collection",
	Long: `Re-pulls the collection index and overwrites the local cache under
.intropy/collections/<alias>.json.

Without --ref, the stored ref in skills.json is re-pulled in place (useful if
the upstream tag is a moving tag like :latest or if the collection was
republished under the same tag).

With --ref, the stored ref is replaced with the new value and the cache is
refreshed from it — use this to bump a registered collection from one
version tag to another.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFlag(skillsCollectionUpdateOpts.output, "json", "plain"); err != nil {
			return err
		}
		alias := args[0]

		project, err := skill.FindProject(".")
		if err != nil {
			return fmt.Errorf("collection update: %w", err)
		}

		manifest, err := project.LoadManifest()
		if err != nil {
			return fmt.Errorf("collection update: load manifest: %w", err)
		}

		idx := -1
		for i, c := range manifest.Collections {
			if c.Name == alias {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("collection update: no collection registered as %q", alias)
		}

		ref := manifest.Collections[idx].Ref
		if skillsCollectionUpdateOpts.ref != "" {
			if _, err := oci.ParseReference(skillsCollectionUpdateOpts.ref); err != nil {
				return fmt.Errorf("collection update: invalid ref %q: %w", skillsCollectionUpdateOpts.ref, err)
			}
			ref = skillsCollectionUpdateOpts.ref
		}

		client, err := newSkillRegistry()
		if err != nil {
			return fmt.Errorf("collection update: %w", err)
		}

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		index, err := client.PullIndex(ctx, ref)
		if err != nil {
			return fmt.Errorf("collection update: fetch index: %w", err)
		}
		cached := &skill.CachedCollection{
			Ref:       ref,
			FetchedAt: time.Now().UTC(),
			Index:     index,
		}
		if err := project.SaveCollectionCache(alias, cached); err != nil {
			return fmt.Errorf("collection update: cache index: %w", err)
		}

		if manifest.Collections[idx].Ref != ref {
			manifest.Collections[idx].Ref = ref
			if err := project.SaveManifest(manifest); err != nil {
				return fmt.Errorf("collection update: save manifest: %w", err)
			}
		}

		if skillsCollectionUpdateOpts.output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"alias":        alias,
				"ref":          ref,
				"cachedSkills": len(index.Manifests),
			})
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "refreshed collection %s from %s\n", alias, ref)
		fmt.Fprintf(cmd.ErrOrStderr(), "  cached %d skill(s)\n", len(index.Manifests))
		return nil
	},
}

func init() {
	skillsCollectionUpdateCmd.Flags().StringVar(
		&skillsCollectionUpdateOpts.ref, "ref", "",
		"Replace the registered ref with this OCI reference before refreshing",
	)
	skillsCollectionUpdateCmd.Flags().StringVarP(&skillsCollectionUpdateOpts.output, "output", "o", "plain", flagUsageOutput)
	skillsCollectionCmd.AddCommand(skillsCollectionUpdateCmd)
}
