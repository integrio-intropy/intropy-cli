package deploy

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/interactive"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// resolveLocalPortBindings validates explicit choices and asks for any
// missing ones when terminal interaction is available. Choices are render
// inputs only: local rendering never persists them.
func resolveLocalPortBindings(ctx context.Context, opts manifestRunOptions, facts manifestFacts, lib *template.Library) (map[string]map[string]string, error) {
	fixtures, err := fixtureCatalog(lib)
	if err != nil {
		return nil, err
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("%s declares no fixture catalog (spec.local.fixtures on %s)\nuse --template-version to pin a library release that ships one", lib.Ref(), TemplateDeployComponent)
	}

	explicit, err := parsePortBindingArgs(opts.Bindings)
	if err != nil {
		return nil, err
	}
	ports := make(map[string]ManifestPort, len(facts.Model.Ports))
	for _, port := range facts.Model.Ports {
		ports[port.Name] = port
	}
	for _, name := range slices.Sorted(maps.Keys(explicit)) {
		fixture := explicit[name]
		if _, ok := ports[name]; !ok {
			return nil, fmt.Errorf("--binding names port %q, which the topology does not declare", name)
		}
		if !slices.Contains(fixtures, fixture) {
			return nil, fmt.Errorf("port %s uses unsupported local fixture %q; available fixtures: %s", name, fixture, strings.Join(fixtures, ", "))
		}
	}

	resolved := make(map[string]map[string]string, len(facts.Model.Ports))
	var missing []string
	for _, port := range facts.Model.Ports {
		fixture := explicit[port.Name]
		if fixture == "" && opts.Selector != nil {
			fixture, err = selectPortBinding(ctx, opts.Selector, port, fixtures)
			if err != nil {
				return nil, fmt.Errorf("select local binding for port %s: %w", port.Name, err)
			}
		}
		if fixture == "" {
			missing = append(missing, port.Name)
			continue
		}
		if !slices.Contains(fixtures, fixture) {
			return nil, fmt.Errorf("port %s uses unsupported local fixture %q; available fixtures: %s",
				port.Name, fixture, strings.Join(fixtures, ", "))
		}
		resolved[port.Name] = map[string]string{localEnv: fixture}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("local bindings are required for ports: %s\npass one '--binding <port>=<fixture>' for each; available fixtures: %s",
			strings.Join(missing, ", "), strings.Join(fixtures, ", "))
	}
	return resolved, nil
}

func parsePortBindingArgs(args []string) (map[string]string, error) {
	bindings := make(map[string]string, len(args))
	for _, arg := range args {
		name, fixture, ok := strings.Cut(arg, "=")
		name, fixture = strings.TrimSpace(name), strings.TrimSpace(fixture)
		if !ok || name == "" || fixture == "" {
			return nil, fmt.Errorf("invalid --binding %q; use --binding <port>=<fixture>", arg)
		}
		if _, duplicate := bindings[name]; duplicate {
			return nil, fmt.Errorf("--binding specifies port %s more than once", name)
		}
		bindings[name] = fixture
	}
	return bindings, nil
}

func selectPortBinding(ctx context.Context, selector interactive.Selector, port ManifestPort, fixtures []string) (string, error) {
	options := make([]interactive.SelectOption, 0, len(fixtures))
	for _, fixture := range fixtures {
		options = append(options, interactive.SelectOption{
			Label: fixtureLabel(fixture),
			Value: fixture,
		})
	}
	return selector.Select(ctx, interactive.SelectRequest{
		Title:       "local binding for " + port.Name,
		Description: portDescription(port),
		Options:     options,
	})
}

func portDescription(port ManifestPort) string {
	var details []string
	if port.ExternalSystem != "" {
		details = append(details, "external system "+port.ExternalSystem)
	}
	if len(port.AppIDs) > 0 {
		details = append(details, "used by "+strings.Join(port.AppIDs, ", "))
	}
	if len(details) == 0 {
		return "choose the fixture this port uses locally"
	}
	return strings.Join(details, "; ")
}

func fixtureLabel(fixture string) string {
	description := map[string]string{
		"blob": "S3 object store",
		"file": "local directory",
		"http": "HTTP stub",
		"sftp": "SFTP server",
		"smb":  "SMB share",
	}[fixture]
	if description == "" {
		return fixture
	}
	return fixture + " — " + description
}

// emptyGitOpsBindings keeps port binding types unset during create-only
// onboarding. The generated placeholder becomes ordinary GitOps source for the
// reviewer to complete.
func emptyGitOpsBindings(opts manifestRunOptions, facts manifestFacts) map[string]map[string]string {
	for _, env := range facts.Environments {
		for _, port := range facts.Model.Ports {
			fmt.Fprintf(opts.Stderr, "note: port %s has no binding for %s; its manifests keep the REPLACE-ME scaffold\n", port.Name, env)
		}
	}
	return map[string]map[string]string{}
}

// fixtureCatalog reads the closed local fixture catalog from the fetched
// deploy-component template.
func fixtureCatalog(lib *template.Library) ([]string, error) {
	tmpl, _, err := lib.Open(TemplateDeployComponent)
	if err != nil {
		return nil, err
	}
	if tmpl.Spec.Local == nil {
		return nil, nil
	}
	return tmpl.Spec.Local.Fixtures, nil
}
