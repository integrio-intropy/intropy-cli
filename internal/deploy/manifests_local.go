package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"gopkg.in/yaml.v3"
)

// kustomizeBuild is the seam unit tests replace to avoid the kustomize
// binary; production wires kustomize.Client.Build.
var kustomizeBuild = func(ctx context.Context, runner command.Runner, dir string) ([]byte, error) {
	return kustomize.Client{Runner: runner}.Build(ctx, dir)
}

// localEnv is the only environment a local render knows. It names the overlay
// the templates ship for the local cluster and lands in .gitops.env.
const localEnv = "local"

// localRegistry is the placeholder registry a local render reports. Every
// Deployment image is overridden by the root kustomization's images[] entries,
// so the rendered registry is never pulled from.
const localRegistry = "dev"

// localImageTag is the conventional tag the k3s scripts build and load
// component images under.
const localImageTag = "dev"

// localImagePrefix is the repository prefix the k3s scripts tag every
// side-loaded image under, and the prefix the templates pin in the local
// overlay's image reference (local/<component>). It keeps a locally built
// image out of any registry default a bare name would resolve to.
const localImagePrefix = "local/"

// localExclusionReason is the when-condition on the repo-metadata exclusion
// rules a local render prepends. It is the literal "false": the filter's
// truthy() treats any other non-empty string as true, so a prose reason here
// would *include* the matched files — the bug that let component.yaml and
// every conditional variant into local renders.
const localExclusionReason = "false"

// gitOpsFileRules excludes the local-only overlay from GitOps scaffolds. A
// static overlays/local skeleton would otherwise render alongside every
// deployable environment despite local not being a GitOps environment.
func gitOpsFileRules(rules []template.FileRule) []template.FileRule {
	return append([]template.FileRule{{Path: "overlays/local/**", When: localExclusionReason}}, rules...)
}

// localFileRules narrows a template's file rules to manifests only: an
// exclusion for the repo-metadata paths, prepended so it decides first,
// ahead of the template's own rules. Those rules are kept — with the local
// facts seeded (pubsub rabbitmq, the block's workload, secretStore kubernetes)
// they select the same variants a local render needs, and they are what keeps
// the generic overlays/{{ .env }}/ skeleton from rendering over the local
// overlay.
func localFileRules(rules []template.FileRule) []template.FileRule {
	out := make([]template.FileRule, 0, len(localRepoMetadataGlobs)+len(rules))
	for _, glob := range localRepoMetadataGlobs {
		out = append(out, template.FileRule{Path: glob, When: localExclusionReason})
	}
	return append(out, rules...)
}

// localRepoMetadataGlobs are the skeleton paths a local render never emits:
// repo metadata the rest of deploy reads, not Kubernetes manifests. The
// fixture-contract doc in the templates repo enumerates the same list.
var localRepoMetadataGlobs = []string{"component.yaml.tmpl"}

// localPlatform is the platform the local development cluster runs, standing
// in for deploy.yaml's platform section: the broker and secret store the k3s
// setup scripts install.
var localPlatform = gitops.PlatformConfig{Provider: "kubernetes", Pubsub: "rabbitmq", SecretStore: "kubernetes"}

// newLocalFacts is resolveGitOpsFacts with local constants in place of the GitOps
// facts. Domain and the ArgoCD app namespace stay empty — they have no local
// meaning, and the fixture contract forbids the local overlay's skeletons
// from referencing them. registry is the placeholder the root kustomization's
// images[] entries make irrelevant.
func newLocalFacts(system string, model ManifestModel, scaffolds map[string]template.ScaffoldEntry, selected []ManifestComponent) manifestFacts {
	return manifestFacts{
		System:       system,
		Environments: []string{localEnv},
		Registry:     localRegistry,
		Platform:     localPlatform,
		Model:        model,
		Scaffolds:    scaffolds,
		Selected:     selected,
	}
}

// localModel is the reserved .local structure: the per-port fixture
// bindings decided by this command.
type localModel struct {
	Bindings map[string]string `json:"bindings"`
}

// imageOverride is one parsed --image entry: a per-component newName/newTag
// pair, or a global newTag when Component is empty.
type imageOverride struct {
	Component string
	NewName   string
	NewTag    string
}

// parseImageOverrides parses the --image grammar: "<component>=<name:tag>" for
// one component, ":<tag>" for all of them. Anything else is a usage error
// showing both forms.
func parseImageOverrides(args []string) ([]imageOverride, error) {
	var out []imageOverride
	for _, arg := range args {
		if strings.HasPrefix(arg, ":") {
			tag := strings.TrimPrefix(arg, ":")
			if tag == "" || strings.ContainsAny(tag, "=:") {
				return nil, invalidImageOverride(arg)
			}
			out = append(out, imageOverride{NewTag: tag})
			continue
		}
		component, ref, found := strings.Cut(arg, "=")
		name, tag, tagged := strings.Cut(ref, ":")
		if !found || component == "" || !tagged || name == "" || tag == "" || strings.Contains(tag, ":") {
			return nil, invalidImageOverride(arg)
		}
		out = append(out, imageOverride{Component: component, NewName: name, NewTag: tag})
	}
	return out, nil
}

func invalidImageOverride(arg string) error {
	return fmt.Errorf("invalid --image %q\nvalid forms: --image <component>=<name:tag> to override one component, --image :<tag> to retag every component", arg)
}

// localImageEntry is one images[] entry in the root kustomization.
type localImageEntry struct {
	Name    string `yaml:"name"`
	NewName string `yaml:"newName,omitempty"`
	NewTag  string `yaml:"newTag,omitempty"`
}

// localImageEntries resolves the images[] list: one entry per component, host
// excluded — the same fact requirePinnable encodes (the host is shared and
// declares no image). Each rendered reference is exactly local/<component>,
// the shape the fixture contract pins, so name matching cannot silently miss.
func localImageEntries(facts manifestFacts, overrides []imageOverride) ([]localImageEntry, error) {
	perComponent := map[string]imageOverride{}
	var global *imageOverride
	for _, o := range overrides {
		if o.Component == "" {
			g := o
			global = &g
			continue
		}
		perComponent[o.Component] = o
	}

	known := map[string]bool{}
	for _, c := range facts.Selected {
		known[c.Name] = true
	}
	var names []string
	for name := range perComponent {
		if !known[name] {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		slices.Sort(names)
		return nil, fmt.Errorf("--image names %s, which the topology does not declare as a component", strings.Join(names, ", "))
	}

	entries := make([]localImageEntry, 0, len(facts.Selected))
	for _, c := range facts.Selected {
		e := localImageEntry{Name: localImagePrefix + c.Name, NewTag: localImageTag}
		if o, ok := perComponent[c.Name]; ok {
			e.NewName = o.NewName
			e.NewTag = o.NewTag
		} else if global != nil {
			e.NewTag = global.NewTag
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// localRootKustomization is the root kustomization the CLI writes after
// rendering: the only place namespace and the conventional image tags are
// applied, so templates stay namespace-agnostic and never hardcode a tag.
type localRootKustomization struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Namespace  string            `yaml:"namespace"`
	Resources  []string          `yaml:"resources"`
	Images     []localImageEntry `yaml:"images,omitempty"`
}

func writeLocalRootKustomization(staging, namespace string, facts manifestFacts, images []localImageEntry) error {
	k := localRootKustomization{
		APIVersion: "kustomize.config.k8s.io/v1beta1",
		Kind:       "Kustomization",
		Namespace:  namespace,
		Resources:  []string{HostDirName + "/overlays/" + localEnv},
		Images:     images,
	}
	for _, c := range facts.Selected {
		k.Resources = append(k.Resources, c.Name+"/overlays/"+localEnv)
	}
	data, err := yaml.Marshal(k)
	if err != nil {
		return fmt.Errorf("encode root kustomization: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "kustomization.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("write root kustomization: %w", err)
	}
	return nil
}

// assertAllImagesTagged is the belt-and-braces check against template drift:
// kustomize silently ignores an images[] entry whose name matches nothing, so
// a drifted image-reference convention would otherwise ship an unoverridden —
// and therefore untagged — image with no error until pull time.
func assertAllImagesTagged(manifest []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `yaml:"image"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("parse rendered manifests: %w", err)
		}
		if doc.Kind != "Deployment" {
			continue
		}
		for _, c := range doc.Spec.Template.Spec.Containers {
			if c.Image == "" {
				continue
			}
			if !strings.Contains(c.Image, ":") && !strings.Contains(c.Image, "@") {
				return fmt.Errorf("a Deployment renders image %q without a tag\nkustomize overrides match on the exact rendered name; the local overlay's image reference convention (local/<component>:dev) may have drifted", c.Image)
			}
		}
	}
}
