package deploy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// resolveConnectorBindings decides which Dapr binding each topology connector
// deploys as, for every environment being scaffolded.
//
// The catalog the answers are chosen from and validated against comes from the
// fetched library: spec.local.fixtures for the local environment, spec.bindings
// for every other — so the menu, the validation and the rendered skeletons can
// never drift apart. Recorded answers live in .intropy/deploy-values.yaml and
// are the previous environment's default when a new one is asked: a binding
// type rarely differs between GitOps environments, so a re-ask is usually
// enter-enter-enter.
//
// An unbound connector is never silently skipped. Local mode fails — a fixture
// must bind. GitOps mode binds nothing and reports the connector as pending:
// the skeleton renders its REPLACE-ME scaffold, exactly as before the question
// existed.
func resolveConnectorBindings(opts InitOptions, facts initFacts, lib *template.Library) (map[string]map[string]string, error) {
	if err := migrateLegacyLocalConfig(opts.SourceDir, opts.Stderr); err != nil {
		return nil, err
	}
	path := deployValuesPath(opts.SourceDir)
	vals, err := loadDeployValues(path)
	if err != nil {
		return nil, err
	}
	if vals.Connectors == nil {
		vals.Connectors = map[string]map[string]string{}
	}

	fixtures, bindings, err := bindingCatalogs(lib)
	if err != nil {
		return nil, err
	}

	// NewMenuPrompter rather than SelectPrompter: the connector question is
	// answered from piped stdin in tests and scripts too, and --no-input is
	// still what turns prompting off. The answer is persisted to a checked-in
	// file, so a piped answer is as reviewable as a typed one.
	var prompter *template.MenuPrompter
	if !opts.NoInput {
		prompter = template.NewMenuPrompter(opts.Stdin, opts.Stderr)
	}

	// facts.Environments is promotion order; local mode has exactly one. An
	// answer settled in one environment is the default for the next.
	settled := map[string]string{}
	changed := false
	for _, env := range facts.Environments {
		catalog := bindings
		if env == localEnv {
			catalog = fixtures
		}
		for _, conn := range facts.Model.Connectors {
			if recorded := vals.Connectors[conn.Name][env]; recorded != "" {
				if !slices.Contains(catalog, recorded) {
					return nil, fmt.Errorf("connector %s is bound to %q for %s in %s, which %s does not offer; the catalog is: %s\nedit the file, or delete the entry to be asked again",
						conn.Name, recorded, env, path, TemplateDeployComponent, strings.Join(catalog, ", "))
				}
				settled[conn.Name] = recorded
				continue
			}
			if prompter == nil || len(catalog) == 0 {
				// No menu to offer — an older library — or no prompting at all.
				// Local mode must bind: a fixture is the only binding a local
				// render can deploy. GitOps mode falls back to the placeholder
				// scaffold, exactly as before the question existed.
				if opts.Mode == ModeLocal {
					return nil, fmt.Errorf("connector %s has no local binding in %s\nrun 'intropy deploy init --local %s' interactively, or add it to the file",
						conn.Name, path, facts.System)
				}
				fmt.Fprintf(opts.Stderr, "note: connector %s has no binding for %s; its manifests keep the REPLACE-ME scaffold\n", conn.Name, env)
				continue
			}
			options := catalog
			if prev := settled[conn.Name]; prev != "" && slices.Contains(catalog, prev) {
				options = append([]string{prev}, slices.DeleteFunc(slices.Clone(catalog), func(c string) bool { return c == prev })...)
			}
			heading := fmt.Sprintf("connector %s (external system %s) — which binding for %s?", conn.Name, conn.ExternalSystem, env)
			choice, err := prompter.Select(heading, options)
			if err != nil {
				return nil, fmt.Errorf("read binding for connector %s: %w", conn.Name, err)
			}
			if vals.Connectors[conn.Name] == nil {
				vals.Connectors[conn.Name] = map[string]string{}
			}
			vals.Connectors[conn.Name][env] = choice
			settled[conn.Name] = choice
			changed = true
		}
	}

	// A binding the topology no longer declares is harmless — the state file
	// may be shared with a branch that still has it — but worth a note.
	for name := range vals.Connectors {
		if !slices.ContainsFunc(facts.Model.Connectors, func(c InitConnector) bool { return c.Name == name }) {
			fmt.Fprintf(opts.Stderr, "note: %s binds %s, which the topology no longer declares\n", deployValuesFileName, name)
		}
	}

	if !changed {
		return vals.Connectors, nil
	}
	if err := saveDeployValues(path, vals); err != nil {
		return nil, err
	}
	fmt.Fprintf(opts.Stderr, "recorded connector bindings in %s\n", path)
	return vals.Connectors, nil
}

// bindingCatalogs reads the two closed catalogs from the fetched library. The
// local environment binds to fixtures — the stub servers the k3s scripts
// install — from spec.local.fixtures; every other environment binds to a Dapr
// binding type from spec.bindings. A release without a fixture catalog cannot
// render local bindings, so that is a hard error naming the release. A release
// without spec.bindings is simply older than the GitOps question: those
// environments fall back to placeholders, with no menu to offer.
func bindingCatalogs(lib *template.Library) (fixtures, bindings []string, err error) {
	tmpl, _, err := lib.Open(TemplateDeployComponent)
	if err != nil {
		return nil, nil, err
	}
	if tmpl.Spec.Local == nil || len(tmpl.Spec.Local.Fixtures) == 0 {
		return nil, nil, fmt.Errorf("%s declares no fixture catalog (spec.local.fixtures on %s)\nuse --template-version to pin a library release that ships one", lib.Ref(), TemplateDeployComponent)
	}
	return tmpl.Spec.Local.Fixtures, tmpl.Spec.Bindings, nil
}
