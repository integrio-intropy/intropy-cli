package deploy

import "github.com/Masterminds/semver/v3"

// Release relation values VersionDiff.Relation takes.
const (
	RelationSame   = "same"
	RelationAhead  = "ahead"
	RelationBehind = "behind"
)

// VersionDiff compares two release annotation values in SemVer terms.
type VersionDiff struct {
	// From and To are the annotation values as given, unmodified.
	From, To string

	// Relation is how To compares to From: "same", "ahead" or "behind".
	// Meaningless when Comparable is false.
	Relation string

	// Magnitude is the version component the difference turns on: "patch",
	// "minor" or "major". Empty when the two are the same or not comparable.
	Magnitude string

	// Comparable is false when either side is not a SemVer release: a commit
	// deploy ("@sha"), a bare tag, an empty value. Not comparable is a
	// property of the input, never an error.
	Comparable bool
}

// DiffReleases compares the releases two environments run. Callers pass the
// annotation strings verbatim; a value that is not a SemVer release (a commit
// deploy "@sha", empty, a bare tag) makes the pair not comparable rather than
// an error.
//
// Prereleases parse and compare by SemVer ordering: 1.4.0-rc.1 is behind
// 1.4.0, and the magnitude of a difference comes from the version core, so
// 1.4.0-rc.1 to 1.4.0 is a patch step.
func DiffReleases(from, to string) VersionDiff {
	d := VersionDiff{From: from, To: to}
	fv, err := semver.NewVersion(from)
	if err != nil {
		return d
	}
	tv, err := semver.NewVersion(to)
	if err != nil {
		return d
	}
	d.Comparable = true

	switch c := fv.Compare(tv); {
	case c == 0:
		d.Relation = RelationSame
	case c < 0:
		d.Relation = RelationAhead
	default:
		d.Relation = RelationBehind
	}
	if d.Relation == RelationSame {
		return d
	}

	switch lo, hi := fv, tv; {
	case lo.Major() != hi.Major():
		d.Magnitude = "major"
	case lo.Minor() != hi.Minor():
		d.Magnitude = "minor"
	default:
		d.Magnitude = "patch"
	}
	return d
}
