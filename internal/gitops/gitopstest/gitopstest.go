// Package gitopstest builds GitOps repository fixtures for tests.
//
// It deliberately does not import gitops. The packages under test have
// internal test files (package gitops, package deploy), so importing gitops
// here would create a cycle — and spelling the layout out in literals is
// better anyway: a test that derives its expectations from the code under test
// cannot catch that code changing the layout.
package gitopstest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// DeployYAML is a valid deploy.yaml covering all three sync shapes: an
// automatic environment, one that promotes, and a manual one with a health
// requirement.
const DeployYAML = `schemaVersion: 1
registry: harbor.intropy.io
argocd:
  server: argocd.intropy.io
  appNamespace: customer-fluxia
environments:
  dev:
    sync: auto
  staging:
    sync: auto
    promotesFrom: [dev]
  prod:
    sync: manual
    promotesFrom: [staging]
    requireSourceHealthy: true
`

// BaseDeployment is a Deployment whose container image is the literal IMAGE,
// for callers to substitute.
const BaseDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
spec:
  replicas: 1
  selector:
    matchLabels:
      app: order-extractor
  template:
    metadata:
      labels:
        app: order-extractor
    spec:
      containers:
        - name: app
          image: IMAGE:latest
`

// ComponentYAML renders a component.yaml.
func ComponentYAML(name, image string, envs []string) string {
	return "schemaVersion: 1\nname: " + name +
		"\nsourcePaths: [component/]\nimages:\n  - name: " + image +
		"\nenvironments: [" + strings.Join(envs, ", ") + "]\n"
}

// Component describes one component to place in a fixture repository.
type Component struct {
	// Coordinate is "domain/system/component".
	Coordinate string

	// Image is the repository component.yaml declares and the base references.
	Image string

	// Environments are the overlays to create, and the environments
	// component.yaml declares.
	Environments []string

	// OverlayImages is the images[]/commonAnnotations block appended to the
	// first environment's kustomization.yaml. Empty leaves the overlay
	// unpinned, which is the state most existing repositories are in.
	OverlayImages string
}

// NewRepo creates a git repository laid out as a GitOps repository, with one
// commit containing every component given.
func NewRepo(t *testing.T, components ...Component) string {
	t.Helper()
	root := t.TempDir()
	gittest.Init(t, root, "main")
	gittest.WriteFile(t, filepath.Join(root, "deploy.yaml"), DeployYAML)

	for _, c := range components {
		parts := strings.Split(c.Coordinate, "/")
		if len(parts) != 3 {
			t.Fatalf("coordinate %q must be domain/system/component", c.Coordinate)
		}
		image := c.Image
		if image == "" {
			image = "harbor.intropy.io/integrations/" + parts[2]
		}
		envs := c.Environments
		if len(envs) == 0 {
			envs = []string{"dev"}
		}

		dir := filepath.Join(root, "domains", parts[0], parts[1], parts[2])
		gittest.WriteFile(t, filepath.Join(dir, "component.yaml"), ComponentYAML(parts[2], image, envs))
		gittest.WriteFile(t, filepath.Join(dir, "base", "deployment.yaml"),
			strings.ReplaceAll(BaseDeployment, "IMAGE", image))
		gittest.WriteFile(t, filepath.Join(dir, "base", "kustomization.yaml"),
			"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n")

		for i, env := range envs {
			overlay := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\n"
			if i == 0 {
				overlay += c.OverlayImages
			}
			gittest.WriteFile(t, filepath.Join(dir, "overlays", env, "kustomization.yaml"), overlay)
		}
	}

	gittest.Run(t, root, "add", ".")
	gittest.Run(t, root, "commit", "--quiet", "-m", "onboard components")
	return root
}
