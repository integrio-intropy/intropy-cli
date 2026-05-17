package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/intropy/intropy-cli/internal/blueprint"
	"github.com/intropy/intropy-cli/internal/skill"
	"github.com/spf13/cobra"
)

// completeBlueprints returns completion candidates for blueprint names.
// It fetches the blueprint index from GitHub; on any error it returns
// nothing so completion stays fast and non-blocking.
func completeBlueprints(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	gh := blueprint.NewGitHubClient(nil, "intropy-cli/"+version, "")
	entries, err := gh.ListBlueprints(ctx, "", "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var matches []string
	for _, e := range entries {
		if strings.HasPrefix(e, toComplete) {
			matches = append(matches, e)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

// completeInstalledSkills returns names from the local skills.lock.json.
func completeInstalledSkills(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, err := skill.FindProject(".")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	lockfile, err := project.LoadLockfile()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var matches []string
	for _, s := range lockfile.Skills {
		if strings.HasPrefix(s.Name, toComplete) {
			matches = append(matches, s.Name)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

// completeRegisteredCollections returns aliases from skills.json.
func completeRegisteredCollections(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	project, err := skill.FindProject(".")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	manifest, err := project.LoadManifest()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var matches []string
	for _, c := range manifest.Collections {
		if strings.HasPrefix(c.Name, toComplete) {
			matches = append(matches, fmt.Sprintf("%s\t%s", c.Name, c.Ref))
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}
