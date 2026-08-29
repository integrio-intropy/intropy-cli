package system

import (
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// LoadWorkspaceFacts scans root for scaffold records and indexes what they
// declare into prompt-time workspace facts. Warnings are non-fatal: one
// malformed record must not hide the facts the rest of the workspace
// carries, and a suggestion aid never aborts the create it assists. A
// missing or record-free root yields an empty index.
func LoadWorkspaceFacts(root string) (*template.WorkspaceFacts, []error) {
	entries, warnings := template.ListScaffolds(root)
	return WorkspaceFactsOf(entries), warnings
}

// WorkspaceFactsOf adapts scanned scaffold records into the fact index,
// dropping records that carry no block wiring (shared libraries, system
// hosts) before indexing.
func WorkspaceFactsOf(entries []template.ScaffoldEntry) *template.WorkspaceFacts {
	factEntries := make([]template.WorkspaceFactEntry, 0, len(entries))
	for _, e := range entries {
		if e.BlockKind == "" {
			continue
		}
		factEntries = append(factEntries, template.WorkspaceFactEntry{
			BlockKind: e.BlockKind,
			Values:    e.Values,
		})
	}
	return template.BuildWorkspaceFacts(factEntries)
}
