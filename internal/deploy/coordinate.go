package deploy

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Coordinate locates one component in a GitOps repository.
type Coordinate struct {
	Domain    string
	System    string
	Component string
}

// RelPath returns the component directory relative to the repository root,
// always slash-separated: it is used to build ArgoCD source paths, which are
// repository paths rather than filesystem paths.
func (c Coordinate) RelPath() string {
	return path.Join(DomainsDirName, c.Domain, c.System, c.Component)
}

// OverlayRelPath returns the environment's overlay directory relative to the
// repository root. This is the value ArgoCD records as spec.source.path.
func (c Coordinate) OverlayRelPath(env string) string {
	return path.Join(c.RelPath(), OverlaysDirName, env)
}

// AppName returns the ArgoCD Application name, matching the ApplicationSet
// template that generates one Application per overlay directory:
//
//	name: '{{index .path.segments 1}}-{{index .path.segments 2}}-{{index .path.segments 3}}-{{.cluster}}'
//
// where the environment is the cluster element.
func (c Coordinate) AppName(env string) string {
	return strings.Join([]string{c.Domain, c.System, c.Component, env}, "-")
}

// String renders the coordinate in the form used in messages.
func (c Coordinate) String() string {
	return c.Domain + "/" + c.System + "/" + c.Component
}

// ErrComponentNotFound reports that no component directory matched.
var ErrComponentNotFound = errors.New("component not found")

// AmbiguousComponentError reports that a component name occurs under more than
// one domain or system, so the caller must say which one it meant.
type AmbiguousComponentError struct {
	Component string
	Matches   []Coordinate
}

func (e *AmbiguousComponentError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q is ambiguous — it exists in %d places:", e.Component, len(e.Matches))
	for _, m := range e.Matches {
		fmt.Fprintf(&b, "\n  %s", m)
	}
	b.WriteString("\npass --domain and --system to choose one")
	return b.String()
}

// FindComponent locates a component in the repository at root.
//
// Rather than requiring the full coordinate, this globs
// domains/*/*/<component> for a directory holding a component.yaml. Deriving
// the domain and system from the caller's working directory would be cheaper
// but couples deploy to a source-repository layout that nothing enforces;
// searching the GitOps repository asks the one place that is authoritative.
//
// domain and system are optional filters. When either is set only matching
// candidates are considered, which is how an ambiguous name is resolved.
func FindComponent(root, component, domain, system string) (Coordinate, error) {
	if component == "" {
		return Coordinate{}, errors.New("no component name given")
	}

	pattern := filepath.Join(root, DomainsDirName, "*", "*", component, ComponentFileName)
	hits, err := filepath.Glob(pattern)
	if err != nil {
		return Coordinate{}, fmt.Errorf("search %s: %w", filepath.Join(root, DomainsDirName), err)
	}

	var matches []Coordinate
	for _, hit := range hits {
		componentDir := filepath.Dir(hit)
		systemDir := filepath.Dir(componentDir)
		domainDir := filepath.Dir(systemDir)
		c := Coordinate{
			Domain:    filepath.Base(domainDir),
			System:    filepath.Base(systemDir),
			Component: filepath.Base(componentDir),
		}
		if domain != "" && c.Domain != domain {
			continue
		}
		if system != "" && c.System != system {
			continue
		}
		matches = append(matches, c)
	}

	slices.SortFunc(matches, func(a, b Coordinate) int {
		return strings.Compare(a.String(), b.String())
	})

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Coordinate{}, notFoundError(root, component, domain, system)
	default:
		return Coordinate{}, &AmbiguousComponentError{Component: component, Matches: matches}
	}
}

// notFoundError distinguishes "this component is not in the repository at all"
// from "it is there, but not where you said it was", because the fix differs.
func notFoundError(root, component, domain, system string) error {
	if domain != "" || system != "" {
		if all, err := FindComponent(root, component, "", ""); err == nil {
			return fmt.Errorf("%w: %q exists at %s, not under the domain/system given", ErrComponentNotFound, component, all)
		}
	}
	detail := fmt.Sprintf("no %s for %q under %s/", ComponentFileName, component, DomainsDirName)
	if names := listComponents(root); len(names) > 0 {
		detail += "\nthe repository defines: " + strings.Join(names, ", ")
	} else {
		detail += fmt.Sprintf("\nthe component must be onboarded first: it needs %s/<domain>/<system>/%s/{%s,base,overlays}", DomainsDirName, component, ComponentFileName)
	}
	return fmt.Errorf("%w: %s", ErrComponentNotFound, detail)
}

// ListComponents returns every component in the repository, sorted. It backs
// shell completion and the "did you mean" list, so it never fails: an
// unreadable repository yields no suggestions rather than an error.
func ListComponents(root string) []Coordinate {
	hits, err := filepath.Glob(filepath.Join(root, DomainsDirName, "*", "*", "*", ComponentFileName))
	if err != nil {
		return nil
	}
	out := make([]Coordinate, 0, len(hits))
	for _, hit := range hits {
		componentDir := filepath.Dir(hit)
		systemDir := filepath.Dir(componentDir)
		out = append(out, Coordinate{
			Domain:    filepath.Base(filepath.Dir(systemDir)),
			System:    filepath.Base(systemDir),
			Component: filepath.Base(componentDir),
		})
	}
	slices.SortFunc(out, func(a, b Coordinate) int {
		return strings.Compare(a.String(), b.String())
	})
	return out
}

func listComponents(root string) []string {
	seen := map[string]bool{}
	var names []string
	for _, c := range ListComponents(root) {
		if !seen[c.Component] {
			seen[c.Component] = true
			names = append(names, c.Component)
		}
	}
	return names
}

// ResolveOverlay validates that a component can be deployed to env and returns
// the overlay's absolute path.
//
// Both the component's declared environment list and the overlay directory are
// checked. They can disagree — a declared environment with no overlay, or an
// overlay nobody declared — and each mismatch is worth reporting distinctly
// rather than as a bare "not found".
func ResolveOverlay(root string, c Coordinate, comp *ComponentConfig, env string) (string, error) {
	dir := filepath.Join(root, filepath.FromSlash(c.OverlayRelPath(env)))

	if !comp.SupportsEnvironment(env) {
		return "", fmt.Errorf("%s does not declare environment %q in %s; it declares: %s",
			c, env, ComponentFileName, strings.Join(comp.Environments, ", "))
	}

	info, err := os.Stat(dir)
	switch {
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("%s is not a directory", dir)
	case err != nil:
		available := listOverlays(root, c)
		if len(available) == 0 {
			return "", fmt.Errorf("%s has no %s/ directory, so it is not onboarded to any environment yet", c, OverlaysDirName)
		}
		return "", fmt.Errorf("%s has no overlay for %q; it has overlays for: %s",
			c, env, strings.Join(available, ", "))
	}
	return dir, nil
}

func listOverlays(root string, c Coordinate) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(c.RelPath()), OverlaysDirName))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}
