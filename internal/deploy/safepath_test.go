package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlink is deliberately the raw syscall rather than a helper that validates
// anything: these tests exist because a GitOps repository can contain links this
// CLI would never create.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// apply runs the whole classify-then-write sequence the way Init does, returning
// the first error either pass produced. Which of the two refuses a hostile tree
// is an implementation detail; that nothing is written is not.
func apply(t *testing.T, staging, dest string, actions []FileAction) error {
	t.Helper()
	_, err := applyStaged(staging, destTreeFor(t, dest), actions)
	return err
}

func TestClassifyRefusesASymlinkedDestinationFile(t *testing.T) {
	staging, dest, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/component.yaml": "scaffolded\n"})
	writeTree(t, outside, map[string]string{"secrets.yaml": "not ours\n"})
	symlink(t, filepath.Join(outside, "secrets.yaml"), filepath.Join(dest, "host", "component.yaml"))

	rels, err := stageRels(staging)
	if err != nil {
		t.Fatal(err)
	}
	_, err = classifyStaged(staging, destTreeFor(t, dest), rels)
	if err == nil {
		t.Fatal("expected a symlinked destination to be refused")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention the symlink: %v", err)
	}
	if got := readTreeFile(t, outside, "secrets.yaml"); got != "not ours\n" {
		t.Errorf("the link target was written: %q", got)
	}
}

// The dangerous case is a link on a parent directory, which would be followed
// while creating the file underneath it without the destination guard.
func TestApplyRefusesASymlinkedParentDirectoryWithoutForce(t *testing.T) {
	staging, dest, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/base/kustomization.yaml": "scaffolded\n"})
	symlink(t, outside, filepath.Join(dest, "host"))

	actions := []FileAction{{Rel: "host/base/kustomization.yaml", Action: ActionCreate}}
	err := apply(t, staging, dest, actions)
	if err == nil {
		t.Fatal("expected a symlinked parent directory to be refused")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention the symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "base")); !os.IsNotExist(err) {
		t.Errorf("wrote through the parent symlink into %s", outside)
	}
}

// A link that stays inside the checkout is still a refusal: os.Root's containment
// permits it, and .git/hooks is inside the checkout.
func TestApplyRefusesAParentSymlinkPointingIntoTheRepository(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/base/kustomization.yaml": "scaffolded\n"})
	if err := os.MkdirAll(filepath.Join(dest, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join("..", ".git", "hooks"), filepath.Join(dest, "host"))

	err := apply(t, staging, dest, []FileAction{{Rel: "host/base/kustomization.yaml", Action: ActionCreate}})
	if err == nil {
		t.Fatal("expected a symlink into .git to be refused")
	}
	if _, err := os.Stat(filepath.Join(dest, ".git", "hooks", "base")); !os.IsNotExist(err) {
		t.Error("wrote into .git/hooks")
	}
}

func TestApplyRefusesRelsThatLeaveTheRepository(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"escaped.yaml": "before\n"})

	for _, rel := range []string{
		"../escaped.yaml",
		"host/../../escaped.yaml",
		filepath.Join(outside, "escaped.yaml"),
		"",
	} {
		staging, dest := t.TempDir(), t.TempDir()
		// The staged source does not need to exist: the destination is refused
		// before anything is read.
		err := apply(t, staging, dest, []FileAction{{Rel: rel, Action: ActionCreate}})
		if err == nil {
			t.Errorf("rel %q was accepted", rel)
		}
	}
	if got := readTreeFile(t, outside, "escaped.yaml"); got != "before\n" {
		t.Errorf("a file outside the repository was written: %q", got)
	}
}

func TestStageRelsRefusesASymlinkInTheRenderedTree(t *testing.T) {
	staging, outside := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/component.yaml": "fine\n"})
	writeTree(t, outside, map[string]string{"id_rsa": "private\n"})
	symlink(t, filepath.Join(outside, "id_rsa"), filepath.Join(staging, "host", "leak.yaml"))

	if _, err := stageRels(staging); err == nil {
		t.Fatal("expected a symlink in the staging tree to be refused")
	}
}

// A tree with no links writes missing files through the guarded destination and
// leaves an existing identical file alone.
func TestApplyCreatesThroughTheGuard(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"host/component.yaml":                  "old\n",
		"host/overlays/dev/kustomization.yaml": "fresh\n",
	})
	writeTree(t, dest, map[string]string{"host/component.yaml": "old\n"})

	written, err := applyStaged(staging, destTreeFor(t, dest), classify(t, staging, dest))
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("written = %v", written)
	}
	if got := readTreeFile(t, dest, "host/component.yaml"); got != "old\n" {
		t.Errorf("existing file = %q", got)
	}
	if got := readTreeFile(t, dest, "host/overlays/dev/kustomization.yaml"); got != "fresh\n" {
		t.Errorf("create = %q", got)
	}
}

func TestAssertPathSegment(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../etc", "sales/orders", `sales\orders`, ".hidden"} {
		if err := assertPathSegment("--domain", name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
	for _, name := range []string{"sales", "order-sync", "order_sync2"} {
		if err := assertPathSegment("--domain", name); err != nil {
			t.Errorf("%q was refused: %v", name, err)
		}
	}
}
