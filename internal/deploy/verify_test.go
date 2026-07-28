package deploy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func TestAssertOnlyAcceptsASubset(t *testing.T) {
	expected := []string{"domains/sales/orders/host/component.yaml", "domains/sales/orders/host/base/kustomization.yaml"}

	// Exactly what was asked for.
	if err := assertOnly("staged", expected, expected); err != nil {
		t.Errorf("the expected set was refused: %v", err)
	}
	// Fewer: a file whose content already matched stages nothing, which is normal.
	if err := assertOnly("staged", expected, expected[:1]); err != nil {
		t.Errorf("a subset was refused: %v", err)
	}
	if err := assertOnly("staged", expected, nil); err != nil {
		t.Errorf("an empty set was refused: %v", err)
	}
}

func TestAssertOnlyNamesEveryExtraPath(t *testing.T) {
	err := assertOnly("committed", []string{"wanted.yaml"}, []string{"wanted.yaml", "stray.yaml", ".git/hooks/pre-push"})
	if err == nil {
		t.Fatal("expected the extra paths to be refused")
	}
	for _, want := range []string{"stray.yaml", ".git/hooks/pre-push", "committed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "wanted.yaml") {
		t.Errorf("the expected path should not be reported as extra: %v", err)
	}
}

// The two checks catch different things, and a commit is the half that matters:
// the index is empty by the time a commit exists, so nothing but this would notice
// a commit that came out wider than the index it was made from.
func TestAssertCommittedIsExactlyReadsTheCommit(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := git.Client{Runner: command.ExecRunner{}, Dir: dir}
	ctx := context.Background()

	gittest.WriteFile(t, filepath.Join(dir, "wanted.yaml"), "ours\n")
	gittest.WriteFile(t, filepath.Join(dir, "stray.yaml"), "not ours\n")
	gittest.Run(t, dir, "add", "--", "wanted.yaml", "stray.yaml")
	gittest.Run(t, dir, "commit", "--quiet", "-m", "wider than asked for")

	// The index is clean now, so the staged half has nothing to report.
	if err := assertStagedIsExactly(ctx, g, []string{"wanted.yaml"}); err != nil {
		t.Errorf("staged check on a clean index: %v", err)
	}

	err := assertCommittedIsExactly(ctx, g, []string{"wanted.yaml"})
	if err == nil {
		t.Fatal("expected the widened commit to be refused")
	}
	if !strings.Contains(err.Error(), "stray.yaml") {
		t.Errorf("error does not name the extra path: %v", err)
	}

	if err := assertCommittedIsExactly(ctx, g, []string{"wanted.yaml", "stray.yaml"}); err != nil {
		t.Errorf("a commit matching the expected set was refused: %v", err)
	}
}
