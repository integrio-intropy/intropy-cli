package blueprint

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testScaffold() Scaffold {
	return Scaffold{
		SchemaVersion: ScaffoldSchemaVersion,
		Blueprint:     "test-blueprint",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1.2.3",
		Values:        map[string]any{"name": "Orders", "appPort": float64(5001)},
	}
}

func TestScaffoldWriteLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := testScaffold()
	if err := WriteScaffold(root, want); err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}

	got, err := LoadScaffold(filepath.Join(root, filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("LoadScaffold: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip mismatch:\ngot  %#v\nwant %#v", *got, want)
	}
}

func TestFindScaffoldWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := WriteScaffold(root, testScaffold()); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "src", "Process", "Steps")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, gotRoot, err := FindScaffold(nested)
	if err != nil {
		t.Fatalf("FindScaffold: %v", err)
	}
	if got.Blueprint != "test-blueprint" {
		t.Errorf("Blueprint = %q", got.Blueprint)
	}
	// t.TempDir may sit behind a symlink (macOS /tmp); compare resolved paths.
	wantRoot, _ := filepath.EvalSymlinks(root)
	resolvedGotRoot, _ := filepath.EvalSymlinks(gotRoot)
	if resolvedGotRoot != wantRoot {
		t.Errorf("projectRoot = %q, want %q", gotRoot, root)
	}
}

func TestFindScaffoldNotFound(t *testing.T) {
	_, _, err := FindScaffold(t.TempDir())
	if !errors.Is(err, ErrScaffoldNotFound) {
		t.Fatalf("err = %v, want ErrScaffoldNotFound", err)
	}
}

func TestLoadScaffoldRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scaffold.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScaffold(path); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
