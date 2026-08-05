package deploy

import "testing"

func TestDiffReleases(t *testing.T) {
	cases := []struct {
		name       string
		from, to   string
		relation   string
		magnitude  string
		comparable bool
	}{
		{name: "equal", from: "1.4.0", to: "1.4.0", relation: RelationSame, comparable: true},
		{name: "patch ahead", from: "1.4.0", to: "1.4.2", relation: RelationAhead, magnitude: "patch", comparable: true},
		{name: "minor ahead", from: "1.4.0", to: "1.5.0", relation: RelationAhead, magnitude: "minor", comparable: true},
		{name: "major ahead", from: "1.4.0", to: "2.0.0", relation: RelationAhead, magnitude: "major", comparable: true},
		{name: "patch behind", from: "1.4.2", to: "1.4.0", relation: RelationBehind, magnitude: "patch", comparable: true},
		{name: "minor behind", from: "1.4.0", to: "1.3.2", relation: RelationBehind, magnitude: "minor", comparable: true},
		{name: "major behind", from: "2.0.0", to: "1.9.9", relation: RelationBehind, magnitude: "major", comparable: true},

		// A v prefix is conventional, not significant.
		{name: "v-prefixed equals bare", from: "v1.4.0", to: "1.4.0", relation: RelationSame, comparable: true},
		{name: "v-prefixed ahead", from: "1.4.0", to: "v1.5.0", relation: RelationAhead, magnitude: "minor", comparable: true},

		// Prereleases follow SemVer ordering: an rc of a version is behind the
		// version itself, and the magnitude turns on the version core.
		{name: "rc to its release", from: "1.4.0-rc.1", to: "1.4.0", relation: RelationAhead, magnitude: "patch", comparable: true},
		{name: "release to its rc", from: "1.4.0", to: "1.4.0-rc.1", relation: RelationBehind, magnitude: "patch", comparable: true},
		{name: "rc to later rc", from: "1.4.0-rc.1", to: "1.4.0-rc.2", relation: RelationAhead, magnitude: "patch", comparable: true},

		// Not comparable is a property of the input, never an error.
		{name: "empty from", from: "", to: "1.4.0", comparable: false},
		{name: "empty to", from: "1.4.0", to: "", comparable: false},
		{name: "commit deploy from", from: "@197a3ae", to: "1.4.0", comparable: false},
		{name: "commit deploy to", from: "1.4.0", to: "@197a3ae", comparable: false},
		{name: "bare tag", from: "latest", to: "1.4.0", comparable: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffReleases(tc.from, tc.to)
			if got.From != tc.from || got.To != tc.to {
				t.Errorf("From/To = %q/%q, want %q/%q", got.From, got.To, tc.from, tc.to)
			}
			if got.Comparable != tc.comparable {
				t.Fatalf("Comparable = %v, want %v", got.Comparable, tc.comparable)
			}
			if got.Relation != tc.relation {
				t.Errorf("Relation = %q, want %q", got.Relation, tc.relation)
			}
			if got.Magnitude != tc.magnitude {
				t.Errorf("Magnitude = %q, want %q", got.Magnitude, tc.magnitude)
			}
		})
	}
}
