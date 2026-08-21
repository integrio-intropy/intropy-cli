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

// resolveLocalPortBindings maps one render's fixture choices to the local
// environment. They are never persisted outside that render.
func resolveLocalPortBindings(ctx context.Context, opts manifestRunOptions, facts manifestFacts, lib *template.Library) (map[string]map[string]string, error) {
	fixtures, err := fixtureCatalog(lib)
	if err != nil {
		return nil, err
	}
	if len(fixtures) == 0 {
		return nil, missingBindingCatalog(lib.Ref(), "fixture", "spec.local.fixtures", TemplateDeployComponent)
	}
	choices, err := resolvePortBindingChoices(ctx, opts, facts, fixtures, "local", "fixture")
	if err != nil {
		return nil, err
	}
	return bindChoicesToEnvironments(choices, []string{localEnv}), nil
}

// resolveGitOpsPortBindings maps binding choices to every GitOps
// environment rendered by create. Local rendering resolves its own choices,
// so the two commands cannot affect one another.
func resolveGitOpsPortBindings(ctx context.Context, opts manifestRunOptions, facts manifestFacts, lib *template.Library) (map[string]map[string]string, error) {
	kinds, err := gitOpsBindingCatalog(lib)
	if err != nil {
		return nil, err
	}
	if len(kinds) == 0 {
		return nil, missingBindingCatalog(lib.Ref(), "GitOps binding", "spec.gitops.bindingKinds", TemplateDeployHost)
	}
	choices, err := resolvePortBindingChoices(ctx, opts, facts, kinds, "GitOps", "kind")
	if err != nil {
		return nil, err
	}
	return bindChoicesToEnvironments(choices, facts.Environments), nil
}

// resolvePortBindingChoices validates explicit kinds and asks for the
// missing ones. Its callers deliberately supply distinct local and GitOps
// catalogs, so the two commands have no shared binding configuration.
func resolvePortBindingChoices(ctx context.Context, opts manifestRunOptions, facts manifestFacts, catalog []string, target, valueLabel string) (map[string]string, error) {
	explicit, err := parsePortBindingArgs(opts.Bindings)
	if err != nil {
		return nil, err
	}
	ports := make(map[string]ManifestPort, len(facts.Model.Ports))
	for _, port := range facts.Model.Ports {
		ports[port.Name] = port
	}
	for _, name := range slices.Sorted(maps.Keys(explicit)) {
		binding := explicit[name]
		if _, ok := ports[name]; !ok {
			return nil, fmt.Errorf("--binding names port %q, which the topology does not declare", name)
		}
		if !slices.Contains(catalog, binding) {
			return nil, fmt.Errorf("port %s uses unsupported %s %q; available %ss: %s", name, valueLabel, binding, valueLabel, strings.Join(catalog, ", "))
		}
	}

	choices := make(map[string]string, len(facts.Model.Ports))
	var missing []string
	for _, port := range facts.Model.Ports {
		binding := explicit[port.Name]
		if binding == "" && opts.Selector != nil {
			binding, err = selectPortBinding(ctx, opts.Selector, port, catalog, target)
			if err != nil {
				return nil, fmt.Errorf("select %s binding for port %s: %w", strings.ToLower(target), port.Name, err)
			}
		}
		if binding == "" {
			missing = append(missing, port.Name)
			continue
		}
		if !slices.Contains(catalog, binding) {
			return nil, fmt.Errorf("port %s uses unsupported %s %q; available %ss: %s",
				port.Name, valueLabel, binding, valueLabel, strings.Join(catalog, ", "))
		}
		choices[port.Name] = binding
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s bindings are required for ports: %s\npass one '--binding <port>=<%s>' for each; available %ss: %s",
			strings.ToLower(target), strings.Join(missing, ", "), valueLabel, valueLabel, strings.Join(catalog, ", "))
	}
	return choices, nil
}

func bindChoicesToEnvironments(choices map[string]string, environments []string) map[string]map[string]string {
	resolved := make(map[string]map[string]string, len(choices))
	for port, binding := range choices {
		perEnvironment := make(map[string]string, len(environments))
		for _, environment := range environments {
			perEnvironment[environment] = binding
		}
		resolved[port] = perEnvironment
	}
	return resolved
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

func selectPortBinding(ctx context.Context, selector interactive.Selector, port ManifestPort, fixtures []string, target string) (string, error) {
	options := make([]interactive.SelectOption, 0, len(fixtures))
	for _, fixture := range fixtures {
		options = append(options, interactive.SelectOption{
			Label: fixtureLabel(fixture),
			Value: fixture,
		})
	}
	return selector.Select(ctx, interactive.SelectRequest{
		Title:       strings.ToLower(target) + " binding for " + port.Name,
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
		return "choose this port's binding kind"
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

// gitOpsBindingCatalog reads the closed GitOps binding-kind catalog from the
// fetched deploy-host template.
func gitOpsBindingCatalog(lib *template.Library) ([]string, error) {
	tmpl, _, err := lib.Open(TemplateDeployHost)
	if err != nil {
		return nil, err
	}
	if tmpl.Spec.GitOps == nil {
		return nil, nil
	}
	return tmpl.Spec.GitOps.BindingKinds, nil
}

func missingBindingCatalog(ref, label, path, templateName string) error {
	return fmt.Errorf("%s declares no %s catalog (%s on %s)\nuse --template-version to pin a library release that ships one", ref, label, path, templateName)
}
