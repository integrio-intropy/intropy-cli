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

func classify(t *testing.T, staging, dest string) []FileAction {
	t.Helper()
	rels, err := stageRels(staging)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := classifyStaged(staging, destTreeFor(t, dest), rels)
	if err != nil {
		t.Fatal(err)
	}
	return actions
}

func TestStageRelsIsSortedAndSlashSeparated(t *testing.T) {
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{
		"shared/overlays/dev/kustomization.yaml": "dev\n",
		"shared/base/kustomization.yaml":         "base\n",
		"shared/component.yaml":                  "comp\n",
	})

	rels, err := stageRels(staging)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"shared/base/kustomization.yaml",
		"shared/component.yaml",
		"shared/overlays/dev/kustomization.yaml",
	}
	if strings.Join(rels, "|") != strings.Join(want, "|") {
		t.Errorf("rels = %v, want %v", rels, want)
	}
}

func TestClassifyFreshOnboardingIsAllCreate(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"shared/component.yaml":          "kind: shared\n",
		"shared/base/kustomization.yaml": "resources: []\n",
	})

	for _, a := range classify(t, staging, dest) {
		if a.Action != ActionCreate {
			t.Errorf("%s = %q, want create", a.Rel, a.Action)
		}
	}
}

// A re-run must be a genuine no-op, not a stream of overwrites that churn the
// git history.
func TestClassifyIdenticalFileIsNotAChange(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"shared/component.yaml": "kind: shared\n"})
	writeTree(t, dest, map[string]string{"shared/component.yaml": "kind: shared\n"})

	actions := classify(t, staging, dest)
	if got := actionFor(t, actions, "shared/component.yaml"); got != ActionIdentical {
		t.Errorf("action = %q, want identical", got)
	}
	for _, a := range actions {
		if a.Writes() {
			t.Errorf("%s would be written", a.Rel)
		}
	}
}

func TestClassifyDifferingFileIsAConflict(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"shared/component.yaml": "kind: shared\n"})
	writeTree(t, dest, map[string]string{"shared/component.yaml": "kind: shared\n# hand-edited\n"})

	if got := actionFor(t, classify(t, staging, dest), "shared/component.yaml"); got != ActionConflict {
		t.Errorf("action = %q, want conflict", got)
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

	actions := classify(t, staging, dest)
	if got := actionFor(t, actions, "order-extract/component.yaml"); got != ActionIdentical {
		t.Errorf("existing component = %q, want identical", got)
	}
	if got := actionFor(t, actions, "order-load/component.yaml"); got != ActionCreate {
		t.Errorf("new component = %q, want create", got)
	}
}

func TestApplyStagedRefusesAFileCreatedAfterClassification(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{"shared/component.yaml": "generated\n"})
	actions := classify(t, staging, dest)
	writeTree(t, dest, map[string]string{"shared/component.yaml": "appeared later\n"})

	if _, err := applyStaged(staging, destTreeFor(t, dest), actions); err == nil {
		t.Fatal("expected the late file to be refused")
	}
	if got := readTreeFile(t, dest, "shared/component.yaml"); got != "appeared later\n" {
		t.Errorf("late file was replaced: %q", got)
	}
}

func TestApplyStagedWritesOnlyWhatTheActionsSay(t *testing.T) {
	staging, dest := t.TempDir(), t.TempDir()
	writeTree(t, staging, map[string]string{
		"a/component.yaml": "created\n",
		"b/component.yaml": "not written\n",
	})
	writeTree(t, dest, map[string]string{"b/component.yaml": "left alone\n"})

	actions := classify(t, staging, dest)
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

func TestSummariseActions(t *testing.T) {
	got := summariseActions([]FileAction{
		{Rel: "a", Action: ActionCreate},
		{Rel: "b", Action: ActionCreate},
		{Rel: "c", Action: ActionIdentical},
		{Rel: "d", Action: ActionConflict},
	})
	if got != "2 create, 1 conflict, 1 identical" {
		t.Errorf("summary = %q", got)
	}
	if got := summariseActions(nil); got != "nothing to do" {
		t.Errorf("empty summary = %q", got)
	}
}

func TestComponentRelPathIsSlashSeparated(t *testing.T) {
	if got := componentRelPath("sales", "ordersync", "shared"); got != "domains/sales/ordersync/shared" {
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

// The shared directory owns the system's Dapr Components, so no component may
// render into it, under its current name or the one CLI 2.2 used.
func TestComponentNamesOfTheSharedDirectoryAreReserved(t *testing.T) {
	for _, name := range []string{SharedDirName, LegacyHostDirName} {
		if err := assertComponentNameFree(name); err == nil {
			t.Errorf("assertComponentNameFree(%q) = nil, want an error", name)
		}
	}
	if err := assertComponentNameFree("erp-loader"); err != nil {
		t.Errorf("assertComponentNameFree(erp-loader) = %v", err)
	}
}
