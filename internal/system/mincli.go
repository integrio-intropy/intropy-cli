package system

import (
	"fmt"

	"github.com/Masterminds/semver/v3"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// checkMinCLI returns an OnManifest hook that refuses to render a template
// whose spec.minCLI exceeds this CLI's build version: the template renders
// from a value contract this build may not assemble. A template without
// minCLI passes, and so does any non-semver build version ("dev", "") —
// local development builds must render every template.
func checkMinCLI(cliVersion string) func(*template.Template) error {
	return func(t *template.Template) error {
		min := t.Spec.MinCLI
		if min == "" {
			return nil
		}
		if _, err := semver.NewVersion(cliVersion); err != nil {
			return nil
		}
		current := semver.MustParse(cliVersion)
		if current.LessThan(semver.MustParse(min)) {
			return fmt.Errorf("template %s requires intropy %s or newer (this is %s)\nupgrade intropy, or render with --template-version <older tag>",
				t.Metadata.Name, min, cliVersion)
		}
		return nil
	}
}
