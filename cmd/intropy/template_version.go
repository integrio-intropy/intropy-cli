package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerTemplateVersionFlag wires --template-version (canonical) and its
// deprecated alias --version onto cmd. canonical and alias must be two
// distinct variables owned by the caller; resolveTemplateVersion folds the
// alias into the canonical one inside RunE.
//
// The alias exists because --version named the template release before the
// CLI grew a release command where --version means the version being
// published — one flag name, two meanings.
func registerTemplateVersionFlag(cmd *cobra.Command, canonical, alias *string) {
	f := cmd.Flags()
	f.StringVar(canonical, "template-version", "", "template release tag (default: latest)")
	f.StringVar(alias, "version", "", "template release tag (default: latest) (deprecated: use --template-version)")
	_ = f.MarkHidden("version")
}

// resolveTemplateVersion merges the deprecated --version alias into the
// canonical --template-version value. Both set with different values is a
// usage error; only the alias set warns on stderr and copies. Safe to call
// unconditionally from RunE: it no-ops on commands lacking the flags.
func resolveTemplateVersion(cmd *cobra.Command, canonical, alias *string) error {
	f := cmd.Flags()
	if f.Lookup("template-version") == nil || f.Lookup("version") == nil {
		return nil
	}
	aliasSet := f.Changed("version")
	canonicalSet := f.Changed("template-version")
	if !aliasSet {
		return nil
	}
	if canonicalSet && *alias != *canonical {
		return newUsageErrorf("cannot combine --version with --template-version (they are the same flag; --version is deprecated)")
	}
	// The alias was used — with or without the canonical spelling — so warn.
	// Matching values via both spellings is redundant, not wrong.
	fmt.Fprintln(cmd.ErrOrStderr(), "warning: --version is deprecated; use --template-version (elsewhere --version prints the CLI's own version or names a release to publish)")
	if !canonicalSet {
		*canonical = *alias
	}
	return nil
}
