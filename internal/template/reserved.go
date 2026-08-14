package template

import (
	"fmt"
	"maps"
	"slices"
)

// Reserved value keys carry structured, non-parameter data into a render.
//
// spec.parameters is scalar-only by design, so a skeleton cannot declare "the
// list of ports" as a parameter. These keys are how it gets one anyway:
// the caller injects the structures after validation, and the skeleton ranges
// over them. Which files a render produces is then decided by a spec.files
// condition over the same data.
const (
	ReservedTopologyKey  = "topology"
	ReservedComponentKey = "component"
	ReservedGitopsKey    = "gitops"
	ReservedScaffoldKey  = "scaffold"

	// ReservedEnvKey is the environment being rendered. Reserved rather than a
	// parameter because the caller supplies it, never the user — and because it
	// is the value most often needed in a *path* (`overlays/{{ .env }}/`), where
	// a nested lookup would read badly.
	ReservedEnvKey = "env"

	// ReservedLocalKey carries the local-render decisions — the per-port
	// fixture bindings — into a render for the local development cluster.
	ReservedLocalKey = "local"
)

// InjectReserved adds structured, non-parameter data to a resolved value map.
//
// It must be called *after* Resolve/ResolveLayered, never before: the values it
// adds are lists and nested maps that spec.parameters cannot describe, so
// running them through JSON Schema validation would fail.
//
// A reserved key that collides with a declared parameter, a spec.values entry,
// or a value already in the map is refused — a template that read one thing
// while its schema documented another would be undebuggable. Nothing is
// injected when any key collides, so a caller never sees a half-populated map.
func InjectReserved(t *Template, values map[string]any, reserved map[string]any) error {
	declared := map[string]string{}
	for _, f := range t.Fields() {
		declared[f.Name] = "a declared parameter"
	}
	for name := range t.Spec.Values {
		declared[name] = "a spec.values entry"
	}

	// Sorted so a template with several collisions always reports the same one.
	keys := slices.Sorted(maps.Keys(reserved))
	for _, key := range keys {
		if what, collides := declared[key]; collides {
			return fmt.Errorf("reserved key %q collides with %s", key, what)
		}
		if _, present := values[key]; present {
			return fmt.Errorf("reserved key %q is already set; it cannot be supplied with --set or a values file", key)
		}
	}
	for _, key := range keys {
		values[key] = reserved[key]
	}
	return nil
}
