package template

import (
	"fmt"
	"path/filepath"
)

// The wiring vocabulary: the parameter names block templates record in
// their scaffold values and later commands (sys create/update, deploy,
// prompt suggestions) read back. They are CLI-owned convention, not
// manifest schema — a template that names its parameters otherwise simply
// gets no assembly or suggestions. One home so a vocabulary change has
// one diff, not a scavenger hunt.
const (
	KeyAppID        = "appId"
	KeyTopic        = "topic"
	KeyContract     = "contract"
	KeyPubsub       = "pubsub"
	KeyPort         = "port"
	KeyFromPort     = "fromPort"
	KeyToPort       = "toPort"
	KeyName         = "name"
	KeyOrganization = "organization"
	KeyProjectName  = "projectName"
	KeySystemClass  = "systemClass"

	// DefaultPubsub is the pub/sub component a record belongs to when it
	// predates the pubsub value being recorded.
	DefaultPubsub = "pubsub"
)

// RecordValue reads key from a scaffold record's values as a non-empty
// string. Missing, mistyped, and empty values are errors naming the
// record, so the user knows which project to fix. It is the strict
// regime: callers validating a workspace (system assembly) use it;
// callers offering suggestions use SoftValue instead.
func RecordValue(e ScaffoldEntry, key string) (string, error) {
	record := filepath.Join(e.Path, filepath.FromSlash(ScaffoldRelPath))
	v, ok := e.Values[key]
	if !ok {
		return "", fmt.Errorf("%s: values.%s is missing", record, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: values.%s has type %T, expected string", record, key, v)
	}
	if s == "" {
		return "", fmt.Errorf("%s: values.%s is empty", record, key)
	}
	return s, nil
}

// RecordValueDefault is RecordValue with a fallback for records that
// predate the value being recorded. Only a missing key falls back; a
// present but mistyped or empty value is still an error — a record that
// names the key must say something meaningful.
func RecordValueDefault(e ScaffoldEntry, key, fallback string) (string, error) {
	if _, ok := e.Values[key]; !ok {
		return fallback, nil
	}
	return RecordValue(e, key)
}

// SoftValue reads key from a values map as a non-empty string, reporting
// false for missing, mistyped, or empty values. It is the lenient regime:
// suggestion aids and best-effort joins skip such records rather than
// failing an operation the records themselves would still allow.
func SoftValue(values map[string]any, key string) (string, bool) {
	s, ok := values[key].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}
