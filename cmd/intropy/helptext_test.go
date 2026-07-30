package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// These tests walk the whole command tree and enforce the writing rules in
// AGENTS.md, so a new command cannot silently re-introduce a wall of help
// text or a malformed Use string.

// maxLongWords is the hard ceiling for a Long description. AGENTS.md targets
// 150 words; this ceiling is looser so the test does not fight legitimate
// detail, but a wall of text trips it.
const maxLongWords = 220

// usePattern enforces: lowercase command name, positional args as <name>,
// optionals as [name], variadic as [name...].
var usePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*( <[a-z][a-z0-9-]*>)*( \[[a-z][a-z0-9-]*(\.\.\.)?\])*$`)

func walkCommands(cmd *cobra.Command, out []*cobra.Command) []*cobra.Command {
	out = append(out, cmd)
	for _, c := range cmd.Commands() {
		out = walkCommands(c, out)
	}
	return out
}

func TestHelpTextLongLength(t *testing.T) {
	for _, cmd := range walkCommands(rootCmd, nil) {
		words := len(strings.Fields(cmd.Long))
		if words > maxLongWords {
			t.Errorf("%s: Long is %d words (max %d) — move rationale to docs/, keep --help to usage",
				cmd.CommandPath(), words, maxLongWords)
		}
	}
}

func TestHelpTextShortStyle(t *testing.T) {
	for _, cmd := range walkCommands(rootCmd, nil) {
		if cmd.Short == "" {
			t.Errorf("%s: Short is empty", cmd.CommandPath())
			continue
		}
		if strings.HasSuffix(cmd.Short, ".") {
			t.Errorf("%s: Short ends with a period (fragments only, per AGENTS.md)", cmd.CommandPath())
		}
		if strings.Contains(cmd.Short, "\n") {
			t.Errorf("%s: Short contains a newline", cmd.CommandPath())
		}
	}
}

func TestHelpTextUsePattern(t *testing.T) {
	for _, cmd := range walkCommands(rootCmd, nil) {
		// cmd.Use is the first line only; cobra may append aliases on later
		// lines, which the pattern does not cover.
		use, _, _ := strings.Cut(cmd.Use, "\n")
		if !usePattern.MatchString(use) {
			t.Errorf("%s: Use %q does not match 'cmd <arg> [opt]...' conventions", cmd.CommandPath(), cmd.Use)
		}
		if strings.Contains(use, "[<") || strings.Contains(use, ">]") {
			t.Errorf("%s: Use %q wraps optional markers around angle brackets; write [name] not [<name>]",
				cmd.CommandPath(), cmd.Use)
		}
	}
}

func TestHelpTextCommandVerbsAreDocumented(t *testing.T) {
	// The verb set documented in AGENTS.md under "Command verbs". A command
	// introducing a verb outside this set fails here; the fix is either to
	// use an existing verb or to document the new one in AGENTS.md first.
	documentedVerbs := map[string]bool{
		"list": true, "show": true, "status": true, "diff": true,
		"create": true, "add": true, "update": true, "init": true,
		"publish": true, "pin": true, "promote": true, "sync": true,
	}
	// Nouns, command groups, and standalone commands that are not verbs and
	// so are not governed by the AGENTS.md verb table.
	notVerbs := map[string]bool{
		"intropy": true, "int": true, "template": true, "skills": true,
		"collection": true, "deploy": true, "release": true, "sys": true,
		"version": true, "dashboard": true,
		// Hidden stub kept so old muscle memory gets a pointer, not an
		// unknown-command error; see int_describe.go.
		"describe": true,
		// Cobra's built-ins and the shell names it registers under
		// 'completion'; none are authored verbs.
		"help": true, "completion": true,
		"bash": true, "zsh": true, "fish": true, "powershell": true,
	}
	for _, cmd := range walkCommands(rootCmd, nil) {
		name := cmd.Name()
		if notVerbs[name] || documentedVerbs[name] {
			continue
		}
		t.Errorf("%s: verb %q is not documented in AGENTS.md 'Command verbs' — use an existing verb or document the new one there",
			cmd.CommandPath(), name)
	}
}

func TestHelpTextSharedFlagsUseConstants(t *testing.T) {
	// The descriptions shared flags must carry, from flagtext.go. A command
	// that re-introduces a hand-written variant fails here.
	want := map[string]string{
		"output":        flagUsageOutput,
		"gitops-repo":   flagUsageGitopsRepo,
		"argocd-server": flagUsageArgocd,
		"domain":        flagUsageDomain,
		"system":        flagUsageSystem,
	}
	// Flags whose meaning legitimately differs on one command get their own
	// constant in flagtext.go; allow those here.
	allowed := map[string]map[string]bool{
		"output": {flagUsageOutput: true, flagUsageOutputJSONOnly: true},
		"domain": {flagUsageDomain: true, flagUsageInitDomain: true},
		"system": {flagUsageSystem: true, flagUsageInitSystem: true},
	}
	for _, cmd := range walkCommands(rootCmd, nil) {
		for name, desc := range want {
			f := cmd.Flags().Lookup(name)
			if f == nil {
				continue
			}
			if ok := allowed[name][f.Usage]; !ok && f.Usage != desc {
				t.Errorf("%s: --%s description is %q, want a shared constant from flagtext.go (e.g. %q)",
					cmd.CommandPath(), name, f.Usage, desc)
			}
		}
	}
}
