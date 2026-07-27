package gitops

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// newGitopsRepo builds a repository tree from coordinates of the form
// "domain/system/component", each with a component.yaml and the given overlays.
func newGitopsRepo(t *testing.T, components map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	gittest.WriteFile(t, filepath.Join(root, DeployFileName), gitopstest.DeployYAML)
	for coord, envs := range components {
		parts := strings.Split(coord, "/")
		if len(parts) != 3 {
			t.Fatalf("coordinate %q must be domain/system/component", coord)
		}
		dir := filepath.Join(root, DomainsDirName, parts[0], parts[1], parts[2])
		gittest.WriteFile(t, filepath.Join(dir, ComponentFileName), componentYAML(parts[2], envs))
		gittest.WriteFile(t, filepath.Join(dir, "base", "kustomization.yaml"), "resources: []\n")
		for _, env := range envs {
			gittest.WriteFile(t, filepath.Join(dir, OverlaysDirName, env, "kustomization.yaml"), "resources:\n  - ../../base\n")
		}
	}
	return root
}

func componentYAML(name string, envs []string) string {
	return "schemaVersion: 1\nname: " + name +
		"\nsourcePaths: [src/" + name + "/]\nimages:\n  - name: harbor.intropy.io/integrations/" + name +
		"\nenvironments: [" + strings.Join(envs, ", ") + "]\n"
}

func TestFindComponent(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{
		"orders/order-flow/order-extractor": {"dev", "prod"},
		"orders/order-flow/erp-loader":      {"dev"},
		"price/distribution/erp-loader":     {"dev"},
	})

	got, err := FindComponent(root, "order-extractor", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	if got != want {
		t.Errorf("FindComponent() = %+v, want %+v", got, want)
	}
}

// The same component name legitimately appears under more than one domain, so
// the error has to name every candidate and say how to choose.
func TestFindComponentAmbiguous(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{
		"orders/order-flow/erp-loader":  {"dev"},
		"price/distribution/erp-loader": {"dev"},
	})

	_, err := FindComponent(root, "erp-loader", "", "")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	amb, ok := errors.AsType[*AmbiguousComponentError](err)
	if !ok {
		t.Fatalf("error %v should be *AmbiguousComponentError", err)
	}
	if len(amb.Matches) != 2 {
		t.Errorf("Matches = %v, want 2", amb.Matches)
	}
	for _, want := range []string{"orders/order-flow/erp-loader", "price/distribution/erp-loader", "--domain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestFindComponentDisambiguatedByFlags(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{
		"orders/order-flow/erp-loader":  {"dev"},
		"price/distribution/erp-loader": {"dev"},
	})

	got, err := FindComponent(root, "erp-loader", "price", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "price" || got.System != "distribution" {
		t.Errorf("FindComponent(--domain price) = %+v", got)
	}

	if got, err = FindComponent(root, "erp-loader", "", "order-flow"); err != nil {
		t.Fatal(err)
	}
	if got.Domain != "orders" {
		t.Errorf("FindComponent(--system order-flow) = %+v", got)
	}
}

func TestFindComponentNotOnboarded(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{"orders/order-flow/order-extractor": {"dev"}})

	_, err := FindComponent(root, "nope", "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrComponentNotFound) {
		t.Errorf("error %v should be ErrComponentNotFound", err)
	}
	// Listing what does exist turns "not found" into something actionable.
	if !strings.Contains(err.Error(), "order-extractor") {
		t.Errorf("error %q should list the components that do exist", err)
	}
}

func TestFindComponentEmptyRepoExplainsOnboarding(t *testing.T) {
	root := t.TempDir()
	_, err := FindComponent(root, "order-extractor", "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "onboarded") {
		t.Errorf("error %q should explain the component must be onboarded", err)
	}
}

// "It exists, but not where you said" needs a different fix from "it does not
// exist", so the two must not share a message.
func TestFindComponentWrongDomainSaysWhereItIs(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{"orders/order-flow/order-extractor": {"dev"}})

	_, err := FindComponent(root, "order-extractor", "price", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "orders/order-flow/order-extractor") {
		t.Errorf("error %q should say where the component actually is", err)
	}
}

func TestCoordinatePaths(t *testing.T) {
	c := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}

	if got, want := c.RelPath(), "domains/orders/order-flow/order-extractor"; got != want {
		t.Errorf("RelPath() = %q, want %q", got, want)
	}
	if got, want := c.OverlayRelPath("dev"), "domains/orders/order-flow/order-extractor/overlays/dev"; got != want {
		t.Errorf("OverlayRelPath() = %q, want %q", got, want)
	}
	// This must match the ApplicationSet's name template exactly, or the
	// ArgoCD lookup 404s on a name nothing generated.
	if got, want := c.AppName("dev"), "orders-order-flow-order-extractor-dev"; got != want {
		t.Errorf("AppName() = %q, want %q", got, want)
	}
}

func TestResolveOverlay(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{"orders/order-flow/order-extractor": {"dev", "prod"}})
	c := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	comp, err := LoadComponentConfig(filepath.Join(root, filepath.FromSlash(c.RelPath())))
	if err != nil {
		t.Fatal(err)
	}

	dir, err := ResolveOverlay(root, c, comp, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "domains", "orders", "order-flow", "order-extractor", "overlays", "dev"); dir != want {
		t.Errorf("ResolveOverlay() = %q, want %q", dir, want)
	}
}

func TestResolveOverlayUndeclaredEnvironment(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{"orders/order-flow/order-extractor": {"dev"}})
	c := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	comp, err := LoadComponentConfig(filepath.Join(root, filepath.FromSlash(c.RelPath())))
	if err != nil {
		t.Fatal(err)
	}

	_, err = ResolveOverlay(root, c, comp, "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "does not declare") || !strings.Contains(err.Error(), "dev") {
		t.Errorf("error %q should say the environment is undeclared and list what is declared", err)
	}
}

// component.yaml and the filesystem can disagree. A declared environment with
// no overlay directory is a distinct mistake from an undeclared one, and gets
// its own message listing the overlays that do exist.
func TestResolveOverlayDeclaredButMissingDirectory(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{"orders/order-flow/order-extractor": {"dev"}})
	c := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	dir := filepath.Join(root, filepath.FromSlash(c.RelPath()))
	gittest.WriteFile(t, filepath.Join(dir, ComponentFileName), componentYAML("order-extractor", []string{"dev", "staging"}))

	comp, err := LoadComponentConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveOverlay(root, c, comp, "staging")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no overlay") || !strings.Contains(err.Error(), "dev") {
		t.Errorf("error %q should report the missing overlay and list those that exist", err)
	}
}

func TestListComponentsIsSortedAndTotal(t *testing.T) {
	root := newGitopsRepo(t, map[string][]string{
		"price/distribution/erp-loader":     {"dev"},
		"orders/order-flow/order-extractor": {"dev"},
		"orders/order-flow/erp-loader":      {"dev"},
	})

	got := ListComponents(root)
	var names []string
	for _, c := range got {
		names = append(names, c.String())
	}
	want := "orders/order-flow/erp-loader,orders/order-flow/order-extractor,price/distribution/erp-loader"
	if strings.Join(names, ",") != want {
		t.Errorf("ListComponents() = %v, want %v", names, want)
	}
}

// Completion must never fail a shell; an unreadable repository yields no
// suggestions.
func TestListComponentsOnMissingRepo(t *testing.T) {
	if got := ListComponents(filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Errorf("ListComponents() = %v, want none", got)
	}
}
