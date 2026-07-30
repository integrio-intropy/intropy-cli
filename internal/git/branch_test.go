//go:build integration

package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func TestCreateBranchAndSwitchBack(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	base, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CreateBranch(ctx, "deploy-init/sales-ordersync", "main"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	gittest.Commit(t, dir, "domains/sales/ordersync/host/component.yaml", "kind: shared\n", "scaffold")

	onBranch, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if onBranch == base {
		t.Fatal("the commit did not land on the new branch")
	}

	if err := g.Switch(ctx, "main"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	back, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back != base {
		t.Errorf("HEAD after switching back = %s, want the original %s", ShortSHA(back), ShortSHA(base))
	}
}

// The only local branches in a cached checkout are ones a previous run left
// behind, so reusing a stale one would build on top of whatever it did.
func TestCreateBranchResetsAnExistingBranch(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	if err := g.CreateBranch(ctx, "deploy-init/x", "main"); err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t, dir, "stale.yaml", "left over\n", "a previous run")

	if err := g.Switch(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	main, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CreateBranch(ctx, "deploy-init/x", "main"); err != nil {
		t.Fatalf("CreateBranch on an existing branch: %v", err)
	}
	got, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != main {
		t.Errorf("HEAD = %s, want the branch reset to main at %s", ShortSHA(got), ShortSHA(main))
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.yaml")); err == nil {
		t.Error("the previous run's file survived the reset")
	}
}

func TestSwitchToMissingRefIsAnError(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	if err := testClient(dir).Switch(context.Background(), "no-such-branch"); err == nil {
		t.Fatal("expected an error for a ref that does not exist")
	}
}
