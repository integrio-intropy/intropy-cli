package template

import "testing"

// TestProductionDefaults pins the values the CLI ships with. The CLI does
// not expose --owner or --repo flags; users always pull from the official
// template library, so accidental changes to these constants must be a
// deliberate, reviewed edit (which updates this test).
func TestProductionDefaults(t *testing.T) {
	if defaultTemplateOwner != "integrio-intropy" {
		t.Errorf("defaultTemplateOwner = %q, want %q", defaultTemplateOwner, "integrio-intropy")
	}
	if defaultTemplateRepo != "intropy-templates" {
		t.Errorf("defaultTemplateRepo = %q, want %q", defaultTemplateRepo, "intropy-templates")
	}
}
