package gitops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DeployFileName is the repository-level contract, at the root of the
	// GitOps repository. Its presence is what marks a repository as one this
	// CLI may deploy through.
	DeployFileName = "deploy.yaml"

	// ComponentFileName is the per-component contract, in the component
	// directory alongside base/ and overlays/.
	ComponentFileName = "component.yaml"

	// DomainsDirName is the root of the coordinate tree.
	DomainsDirName = "domains"

	// OverlaysDirName holds one directory per environment.
	OverlaysDirName = "overlays"

	// SchemaVersion is the newest schema this CLI understands for both files.
	// Bump only for incompatible changes; new optional fields do not require
	// a bump.
	SchemaVersion = 1

	// SyncAuto and SyncManual are the recognised environment sync policies.
	// Manual environments are gated in ArgoCD, so deploy commits and pushes
	// but must not wait for a sync that will never start on its own.
	SyncAuto   = "auto"
	SyncManual = "manual"
)

// DeployConfig is the parsed deploy.yaml.
type DeployConfig struct {
	SchemaVersion int    `yaml:"schemaVersion"`
	Registry      string `yaml:"registry"`

	// MinimumCliVersion, when set, is the oldest CLI the repository's authors
	// consider safe to deploy with.
	MinimumCliVersion string `yaml:"minimumCliVersion"`

	Argocd       ArgocdConfig                 `yaml:"argocd"`
	Environments map[string]EnvironmentConfig `yaml:"environments"`
}

// ArgocdConfig locates the ArgoCD instance that reconciles this repository.
type ArgocdConfig struct {
	Server string `yaml:"server"`

	// AppNamespace is the namespace the Application objects live in. It is not
	// optional in practice: the ApplicationSets are deployed per customer
	// (e.g. customer-wsf) rather than into argocd, and omitting appNamespace
	// from an API call against such an installation returns a 404 that is
	// indistinguishable from "no such application".
	AppNamespace string `yaml:"appNamespace"`
}

// EnvironmentConfig is one entry under environments.
type EnvironmentConfig struct {
	// Sync is "auto" or "manual".
	Sync string `yaml:"sync"`

	// PromotesFrom lists environments a promotion into this one may draw
	// from. Deploy reads it to report whether the digests being deployed are
	// what those environments already run; enforcing it is the promote
	// command's job.
	PromotesFrom []string `yaml:"promotesFrom"`

	// RequireSourceHealthy demands the source environment be healthy before a
	// promotion into this one. Parsed here, enforced by promote.
	RequireSourceHealthy bool `yaml:"requireSourceHealthy"`

	// Scratch marks an environment as disposable. The semantics are not yet
	// defined, so this is carried but never acted on — parsing it now means a
	// repository that already sets it is not rejected as malformed.
	Scratch bool `yaml:"scratch"`
}

// ComponentConfig is the parsed component.yaml.
type ComponentConfig struct {
	SchemaVersion int    `yaml:"schemaVersion"`
	Name          string `yaml:"name"`

	// SourcePaths are the paths in the *source* repository that make up this
	// component. The working-tree cleanliness check is scoped to them, so an
	// unrelated dirty file elsewhere in a monorepo does not block a deploy.
	SourcePaths []string `yaml:"sourcePaths"`

	// Images are the image repositories to pin. Explicit rather than derived:
	// existing repositories name images inconsistently, and a wrong guess
	// silently adds an inert images[] entry instead of failing.
	Images []ImageRef `yaml:"images"`

	// Environments are the environments this component may deploy to.
	Environments []string `yaml:"environments"`
}

// ImageRef names one image repository, without a tag or digest.
type ImageRef struct {
	Name string `yaml:"name"`
}

// LoadDeployConfig reads and validates deploy.yaml from the root of a GitOps
// repository.
func LoadDeployConfig(root string) (*DeployConfig, error) {
	path := filepath.Join(root, DeployFileName)
	var cfg DeployConfig
	if err := decodeFile(path, &cfg); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s has no %s, so it is not a GitOps repository this CLI can deploy through", root, DeployFileName)
		}
		return nil, err
	}
	if err := cfg.validate(path); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *DeployConfig) validate(path string) error {
	if err := checkSchemaVersion(path, c.SchemaVersion); err != nil {
		return err
	}
	if c.Registry == "" {
		return fmt.Errorf("%s: registry is required", path)
	}
	if len(c.Environments) == 0 {
		return fmt.Errorf("%s: at least one entry under environments is required", path)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Environments)) {
		env := c.Environments[name]
		switch env.Sync {
		case SyncAuto, SyncManual:
		case "":
			return fmt.Errorf("%s: environment %q has no sync policy (expected %q or %q)", path, name, SyncAuto, SyncManual)
		default:
			return fmt.Errorf("%s: environment %q has sync %q (expected %q or %q)", path, name, env.Sync, SyncAuto, SyncManual)
		}
		for _, src := range env.PromotesFrom {
			if _, ok := c.Environments[src]; !ok {
				return fmt.Errorf("%s: environment %q promotes from %q, which is not defined", path, name, src)
			}
		}
	}
	return nil
}

// Environment returns the named environment's policy, or an error listing the
// environments the repository does define.
func (c *DeployConfig) Environment(name string) (EnvironmentConfig, error) {
	env, ok := c.Environments[name]
	if !ok {
		return EnvironmentConfig{}, fmt.Errorf("unknown environment %q; %s defines: %s", name, DeployFileName, strings.Join(c.EnvironmentNames(), ", "))
	}
	return env, nil
}

// EnvironmentNames returns the defined environment names in sorted order.
func (c *DeployConfig) EnvironmentNames() []string {
	return slices.Sorted(maps.Keys(c.Environments))
}

// PromotionOrder returns the environments in the order changes flow through
// them: an environment comes after every environment it promotes from. Ties
// break alphabetically, so the order is stable across runs.
//
// EnvironmentNames sorts alphabetically, which renders the usual three as dev,
// prod, staging. That is the wrong story for anything presenting environments
// as a pipeline — the promotion graph is what says prod comes last.
func (c *DeployConfig) PromotionOrder() []string {
	// Sources counts the not-yet-emitted environments each one promotes from.
	// An edge naming an undefined environment is impossible here: validate
	// rejects the file first.
	remaining := make(map[string]int, len(c.Environments))
	for name, env := range c.Environments {
		sources := 0
		for _, src := range env.PromotesFrom {
			// A self-edge is not a dependency on anything else, and counting it
			// would make the environment permanently unemittable.
			if src != name {
				sources++
			}
		}
		remaining[name] = sources
	}

	order := make([]string, 0, len(c.Environments))
	for len(remaining) > 0 {
		ready := make([]string, 0, len(remaining))
		for name, sources := range remaining {
			if sources == 0 {
				ready = append(ready, name)
			}
		}

		// Nothing is ready, so the remainder is a cycle. deploy.yaml's
		// validation does not forbid one, and dropping those environments would
		// hide them from a caller listing what exists — emit them alphabetically
		// instead.
		if len(ready) == 0 {
			order = append(order, slices.Sorted(maps.Keys(remaining))...)
			break
		}

		slices.Sort(ready)
		order = append(order, ready...)
		for _, name := range ready {
			delete(remaining, name)
		}
		// Emitting a whole rank at a time keeps siblings adjacent and
		// alphabetical, rather than interleaving them by discovery order.
		for _, name := range ready {
			for other, env := range c.Environments {
				if _, pending := remaining[other]; !pending {
					continue
				}
				for _, src := range env.PromotesFrom {
					if src == name {
						remaining[other]--
					}
				}
			}
		}
	}
	return order
}

// LoadComponentConfig reads and validates a component.yaml from a component
// directory.
func LoadComponentConfig(dir string) (*ComponentConfig, error) {
	path := filepath.Join(dir, ComponentFileName)
	var cfg ComponentConfig
	if err := decodeFile(path, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(path); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *ComponentConfig) validate(path string) error {
	if err := checkSchemaVersion(path, c.SchemaVersion); err != nil {
		return err
	}
	if c.Name == "" {
		return fmt.Errorf("%s: name is required", path)
	}
	if len(c.Images) == 0 {
		return fmt.Errorf("%s: at least one entry under images is required — there is nothing to pin otherwise", path)
	}
	for i, img := range c.Images {
		if img.Name == "" {
			return fmt.Errorf("%s: images[%d] has no name", path, i)
		}
		if hasTagOrDigest(img.Name) {
			return fmt.Errorf("%s: images[%d] name %q must be a bare repository, without a tag or digest", path, i, img.Name)
		}
	}
	if len(c.Environments) == 0 {
		return fmt.Errorf("%s: at least one entry under environments is required", path)
	}
	return nil
}

// SupportsEnvironment reports whether the component declares env.
func (c *ComponentConfig) SupportsEnvironment(env string) bool {
	return slices.Contains(c.Environments, env)
}

// hasTagOrDigest reports whether an image reference carries a tag or digest.
//
// Only the final path segment is examined, because a colon earlier in the
// reference is a registry port: localhost:5555/integrations/app and
// harbor.example.com:8443/app are perfectly ordinary repositories, and
// rejecting them would rule out every registry not on the default port.
func hasTagOrDigest(image string) bool {
	last := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		last = image[i+1:]
	}
	return strings.ContainsAny(last, ":@")
}

func checkSchemaVersion(path string, got int) error {
	switch {
	case got == 0:
		return fmt.Errorf("%s: schemaVersion is required", path)
	case got > SchemaVersion:
		return fmt.Errorf("%s: schemaVersion %d is newer than this CLI supports (%d); upgrade intropy", path, got, SchemaVersion)
	case got < 1:
		return fmt.Errorf("%s: schemaVersion %d is not valid", path, got)
	}
	return nil
}

// decodeFile decodes a YAML file, rejecting unknown fields so that a
// misspelled key is a loud error rather than a setting that silently does
// nothing.
func decodeFile(path string, into any) error {
	f, err := os.Open(path)
	if err != nil {
		// Wrapped, so callers can still test for fs.ErrNotExist.
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("parse %s: file is empty", path)
		}
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
