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

// resolveLocalConnectorBindings validates explicit choices and asks for any
// missing ones when terminal interaction is available. Choices are render
// inputs only: local rendering never persists them.
func resolveLocalConnectorBindings(ctx context.Context, opts manifestRunOptions, facts manifestFacts, lib *template.Library) (map[string]map[string]string, error) {
	fixtures, err := fixtureCatalog(lib)
	if err != nil {
		return nil, err
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("%s declares no fixture catalog (spec.local.fixtures on %s)\nuse --template-version to pin a library release that ships one", lib.Ref(), TemplateDeployComponent)
	}

	explicit, err := parseConnectorBindingArgs(opts.Bindings)
	if err != nil {
		return nil, err
	}
	connectors := make(map[string]ManifestConnector, len(facts.Model.Connectors))
	for _, connector := range facts.Model.Connectors {
		connectors[connector.Name] = connector
	}
	for _, name := range slices.Sorted(maps.Keys(explicit)) {
		fixture := explicit[name]
		if _, ok := connectors[name]; !ok {
			return nil, fmt.Errorf("--binding names connector %q, which the topology does not declare", name)
		}
		if !slices.Contains(fixtures, fixture) {
			return nil, fmt.Errorf("connector %s uses unsupported local fixture %q; available fixtures: %s", name, fixture, strings.Join(fixtures, ", "))
		}
	}

	resolved := make(map[string]map[string]string, len(facts.Model.Connectors))
	var missing []string
	for _, connector := range facts.Model.Connectors {
		fixture := explicit[connector.Name]
		if fixture == "" && opts.Selector != nil {
			fixture, err = selectConnectorBinding(ctx, opts.Selector, connector, fixtures)
			if err != nil {
				return nil, fmt.Errorf("select local binding for connector %s: %w", connector.Name, err)
			}
		}
		if fixture == "" {
			missing = append(missing, connector.Name)
			continue
		}
		if !slices.Contains(fixtures, fixture) {
			return nil, fmt.Errorf("connector %s uses unsupported local fixture %q; available fixtures: %s",
				connector.Name, fixture, strings.Join(fixtures, ", "))
		}
		resolved[connector.Name] = map[string]string{localEnv: fixture}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("local bindings are required for connectors: %s\npass one '--binding <connector>=<fixture>' for each; available fixtures: %s",
			strings.Join(missing, ", "), strings.Join(fixtures, ", "))
	}
	return resolved, nil
}

func parseConnectorBindingArgs(args []string) (map[string]string, error) {
	bindings := make(map[string]string, len(args))
	for _, arg := range args {
		name, fixture, ok := strings.Cut(arg, "=")
		name, fixture = strings.TrimSpace(name), strings.TrimSpace(fixture)
		if !ok || name == "" || fixture == "" {
			return nil, fmt.Errorf("invalid --binding %q; use --binding <connector>=<fixture>", arg)
		}
		if _, duplicate := bindings[name]; duplicate {
			return nil, fmt.Errorf("--binding specifies connector %s more than once", name)
		}
		bindings[name] = fixture
	}
	return bindings, nil
}

func selectConnectorBinding(ctx context.Context, selector interactive.Selector, connector ManifestConnector, fixtures []string) (string, error) {
	options := make([]interactive.SelectOption, 0, len(fixtures))
	for _, fixture := range fixtures {
		options = append(options, interactive.SelectOption{
			Label: fixtureLabel(fixture),
			Value: fixture,
		})
	}
	return selector.Select(ctx, interactive.SelectRequest{
		Title:       "local binding for " + connector.Name,
		Description: connectorDescription(connector),
		Options:     options,
	})
}

func connectorDescription(connector ManifestConnector) string {
	var details []string
	if connector.ExternalSystem != "" {
		details = append(details, "external system "+connector.ExternalSystem)
	}
	if len(connector.AppIDs) > 0 {
		details = append(details, "used by "+strings.Join(connector.AppIDs, ", "))
	}
	if len(details) == 0 {
		return "choose the fixture this connector uses locally"
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

// emptyGitOpsBindings keeps connector types unset during create-only
// onboarding. The generated placeholder becomes ordinary GitOps source for the
// reviewer to complete.
func emptyGitOpsBindings(opts manifestRunOptions, facts manifestFacts) map[string]map[string]string {
	for _, env := range facts.Environments {
		for _, connector := range facts.Model.Connectors {
			fmt.Fprintf(opts.Stderr, "note: connector %s has no binding for %s; its manifests keep the REPLACE-ME scaffold\n", connector.Name, env)
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
