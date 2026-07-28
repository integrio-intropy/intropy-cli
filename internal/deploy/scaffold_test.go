package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func actionFor(t *testing.T, actions []FileAction, rel string) string {
	t.Helper()
	for _, a := range actions {
		if a.Rel == rel {
			return a.Action
		}
	}
	t.Fatalf("no action for %q in %+v", rel, actions)
	return ""
}

func destTreeFor(t *testing.T, dir string) *destTree {
	t.Helper()
	tree, err := openDestTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree
}

func classify(t *testing.T, staging, dest string, force bool) []FileAction {
	t.Helper()
	rels, err := stageRels(staging)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := classifyStaged(staging, destTreeFor(t, dest), rels, force)
	if err != nil {
		t.Fatal(err)
	}
	return actions
}

func TestStageRelsIsSortedAndSlashSeparated(t *testing.T) {
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{
		"host/overlays/dev/kustomization.yaml": "dev\n",
		"host/base/kustomization.yaml":         "base\n",
		"host/component.yaml":                  "comp\n",
	})

	rels, err := stageRels(staging)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host/base/kustomization.yaml",
		"host/component.yaml",
		"host/overlays/dev/kustomization.yaml",
	}
	if strings.Join(rels, "|") != strings.Join(want, "|") {
		t.Errorf("rels = %v, want %v", rels, want)
	}
}

func TestClassifyFreshOnboardingIsAllCreate(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"host/component.yaml":          "kind: shared\n",
		"host/base/kustomization.yaml": "resources: []\n",
	})

	for _, a := range classify(t, staging, dest, false) {
		if a.Action != ActionCreate {
			t.Errorf("%s = %q, want create", a.Rel, a.Action)
		}
	}
}

// A re-run must be a genuine no-op, not a stream of overwrites that churn the
// git history.
func TestClassifyIdenticalFileIsNotAChange(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/component.yaml": "kind: shared\n"})
	writeTree(t, dest, map[string]string{"host/component.yaml": "kind: shared\n"})

	actions := classify(t, staging, dest, false)
	if got := actionFor(t, actions, "host/component.yaml"); got != ActionIdentical {
		t.Errorf("action = %q, want identical", got)
	}
	for _, a := range actions {
		if a.Writes() {
			t.Errorf("%s would be written", a.Rel)
		}
	}
}

func TestClassifyDifferingFileIsSkipped(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/component.yaml": "kind: shared\n"})
	writeTree(t, dest, map[string]string{"host/component.yaml": "kind: shared\n# hand-edited\n"})

	if got := actionFor(t, classify(t, staging, dest, false), "host/component.yaml"); got != ActionSkipExists {
		t.Errorf("action = %q, want skip-exists", got)
	}
}

func TestClassifyDifferingFileWithForceIsOverwrite(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"host/component.yaml": "new\n"})
	writeTree(t, dest, map[string]string{"host/component.yaml": "old\n"})

	if got := actionFor(t, classify(t, staging, dest, true), "host/component.yaml"); got != ActionOverwrite {
		t.Errorf("action = %q, want overwrite", got)
	}
}

// The property that makes this safe to re-run for a system's whole life: a new
// component is created and every existing one is left alone.
func TestClassifyIsAdditiveWhenTheTopologyGrows(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"order-extract/component.yaml": "existing\n",
		"order-load/component.yaml":    "brand new\n",
	})
	writeTree(t, dest, map[string]string{
		"order-extract/component.yaml": "existing\n",
	})

	actions := classify(t, staging, dest, false)
	if got := actionFor(t, actions, "order-extract/component.yaml"); got != ActionIdentical {
		t.Errorf("existing component = %q, want identical", got)
	}
	if got := actionFor(t, actions, "order-load/component.yaml"); got != ActionCreate {
		t.Errorf("new component = %q, want create", got)
	}
}

func TestApplyStagedWritesOnlyWhatTheActionsSay(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"a/component.yaml": "created\n",
		"b/component.yaml": "not written\n",
	})
	writeTree(t, dest, map[string]string{"b/component.yaml": "left alone\n"})

	actions := classify(t, staging, dest, false)
	written, err := applyStaged(staging, destTreeFor(t, dest), actions)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != "a/component.yaml" {
		t.Fatalf("written = %v", written)
	}
	if got := readTreeFile(t, dest, "a/component.yaml"); got != "created\n" {
		t.Errorf("a = %q", got)
	}
	if got := readTreeFile(t, dest, "b/component.yaml"); got != "left alone\n" {
		t.Errorf("b = %q, want it untouched", got)
	}
}

// Overwriting an overlay that pins a digest un-deploys what it runs, and no user
// means to ask for that.
func TestForceIsRefusedOnAPinnedOverlay(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": "resources:\n  - ../../base\n",
	})
	writeTree(t, dest, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": `resources:
  - ../../base
images:
  - name: harbor.intropy.io/fluxia/order-extract
    digest: sha256:c0ffee
`,
	})

	actions := classify(t, staging, dest, true)
	err := assertForceIsSafe(dest, actions)
	if err == nil {
		t.Fatal("expected --force to be refused")
	}
	for _, want := range []string{"order-extract/overlays/dev/kustomization.yaml", "sha256:c0ffee"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestForceIsAllowedOnAnUnpinnedOverlay(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": "resources:\n  - ../../base\n",
	})
	writeTree(t, dest, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": "resources:\n  - ../../base\nnamespace: integrations\n",
	})

	if err := assertForceIsSafe(dest, classify(t, staging, dest, true)); err != nil {
		t.Errorf("an unpinned overlay should be overwritable: %v", err)
	}
}

// Without --force nothing is overwritten, so a pinned overlay needs no guard.
func TestForceGuardIgnoresSkippedFiles(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": "resources: []\n",
	})
	writeTree(t, dest, map[string]string{
		"order-extract/overlays/dev/kustomization.yaml": "images:\n  - name: x\n    digest: sha256:c0ffee\n",
	})

	if err := assertForceIsSafe(dest, classify(t, staging, dest, false)); err != nil {
		t.Errorf("no overwrite means no guard: %v", err)
	}
}

func TestSummariseActions(t *testing.T) {
	got := summariseActions([]FileAction{
		{Rel: "a", Action: ActionCreate},
		{Rel: "b", Action: ActionCreate},
		{Rel: "c", Action: ActionIdentical},
		{Rel: "d", Action: ActionSkipExists},
	})
	if got != "2 create, 1 skip-exists, 1 identical" {
		t.Errorf("summary = %q", got)
	}
	if got := summariseActions(nil); got != "nothing to do" {
		t.Errorf("empty summary = %q", got)
	}
}

func TestComponentRelPathIsSlashSeparated(t *testing.T) {
	if got := componentRelPath("sales", "ordersync", "host"); got != "domains/sales/ordersync/host" {
		t.Errorf("componentRelPath = %q", got)
	}
}

func readTreeFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
