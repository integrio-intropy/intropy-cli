package main

import (
	"context"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/blueprint"
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
