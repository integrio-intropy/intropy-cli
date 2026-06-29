package blueprint

import "testing"

// TestProductionDefaults pins the values the CLI ships with. The CLI does
// not expose --owner or --repo flags; users always pull from the official
// blueprint library, so accidental changes to these constants must be a
// deliberate, reviewed edit (which updates this test).
func TestProductionDefaults(t *testing.T) {
	if defaultBlueprintOwner != "integrio-intropy" {
		t.Errorf("defaultBlueprintOwner = %q, want %q", defaultBlueprintOwner, "integrio-intropy")
	}
	if defaultBlueprintRepo != "blueprints" {
		t.Errorf("defaultBlueprintRepo = %q, want %q", defaultBlueprintRepo, "blueprints")
	}
}
