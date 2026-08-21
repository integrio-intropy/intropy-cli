package deploy

import (
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

func TestRequirePinnableAllowsService(t *testing.T) {
	coord := gitops.Coordinate{Domain: "sales", System: "ordersync", Component: "order-extract"}
	for _, kind := range []string{"", gitops.KindService} {
		comp := &gitops.ComponentConfig{Name: "order-extract", Kind: kind}
		if err := requirePinnable(coord, comp); err != nil {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
}

// An empty plan would read as "already up to date", so this has to be an error
// rather than a run that changes nothing.
func TestRequirePinnableRejectsShared(t *testing.T) {
	coord := gitops.Coordinate{Domain: "sales", System: "ordersync", Component: "host"}
	comp := &gitops.ComponentConfig{Name: "host", Kind: gitops.KindShared}

	err := requirePinnable(coord, comp)
	if err == nil {
		t.Fatal("expected an error for a shared component")
	}
	for _, want := range []string{"sales/ordersync/host", gitops.KindShared, "nothing to pin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestComponentKindMakesTheDefaultExplicit(t *testing.T) {
	if got := componentKind(&gitops.ComponentConfig{}); got != gitops.KindService {
		t.Errorf("componentKind(implicit) = %q, want %q", got, gitops.KindService)
	}
	if got := componentKind(&gitops.ComponentConfig{Kind: gitops.KindShared}); got != gitops.KindShared {
		t.Errorf("componentKind(shared) = %q, want %q", got, gitops.KindShared)
	}
}

// An empty DIGEST column must not be readable as "never deployed".
func TestNotesExplainSharedHasNoDigest(t *testing.T) {
	coord := gitops.Coordinate{Domain: "sales", System: "ordersync", Component: "host"}
	comp := &gitops.ComponentConfig{Name: "host", Kind: gitops.KindShared}

	got := notes(coord, comp, nil)
	if len(got) == 0 {
		t.Fatal("expected a note for a shared component")
	}
	if !strings.Contains(got[0], "by design") {
		t.Errorf("first note = %q", got[0])
	}
}

func TestNotesSayNothingAboutKindForAService(t *testing.T) {
	coord := gitops.Coordinate{Domain: "sales", System: "ordersync", Component: "order-extract"}
	comp := &gitops.ComponentConfig{
		Name:   "order-extract",
		Images: []gitops.ImageRef{{Name: "harbor.intropy.io/integrations/order-extract"}},
	}
	for _, note := range notes(coord, comp, nil) {
		if strings.Contains(note, "kind") {
			t.Errorf("a service should not be annotated with its kind: %q", note)
		}
	}
}
