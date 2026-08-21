package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadComponentsMissingDir(t *testing.T) {
	if got := readComponents(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("readComponents(missing) = %v, want nil", got)
	}
}

func TestReadComponentsClassifies(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("pubsub.yaml", "metadata:\n  name: pubsub\nspec:\n  type: pubsub.in-memory\n")
	write("lookups.yaml", "metadata:\n  name: lookups\nspec:\n  type: state.in-memory\n")
	// No direction metadata -> direction stays empty (unknown), never guessed.
	write("local-storage.yaml", "metadata:\n  name: local-storage\nspec:\n  type: bindings.localstorage\n")
	write("mystery.yaml", "metadata:\n  name: mystery\nspec:\n  type: secretstores.local.file\n")
	// spec.type absent -> "other", name falls back to file stem.
	write("bare.yml", "metadata:\n  name: bare\n")
	// Direction metadata is normalized: casing, spacing and token order.
	write("orders-in.yaml", "metadata:\n  name: orders-in\nspec:\n  type: bindings.localstorage\n  metadata:\n    - name: rootPath\n      value: ./in\n    - name: direction\n      value: input\n")
	write("orders-out.yaml", "metadata:\n  name: orders-out\nspec:\n  type: bindings.localstorage\n  metadata:\n    - name: direction\n      value: OUTPUT\n")
	write("orders-inout.yaml", "metadata:\n  name: orders-inout\nspec:\n  type: bindings.localstorage\n  metadata:\n    - name: direction\n      value: \"output, input\"\n")
	// Unrecognized direction values surface as unknown, not a guess.
	write("orders-odd.yaml", "metadata:\n  name: orders-odd\nspec:\n  type: bindings.localstorage\n  metadata:\n    - name: direction\n      value: sideways\n")
	// Non-YAML and malformed content are ignored, not surfaced.
	write("readme.txt", "not a component")
	write("broken.yaml", "metadata: [this is not: valid")

	got := readComponents(dir)

	want := map[string]struct{ typ, cat, dir string }{
		"pubsub":        {"pubsub.in-memory", "pubsub", ""},
		"lookups":       {"state.in-memory", "state", ""},
		"local-storage": {"bindings.localstorage", "binding", ""},
		"mystery":       {"secretstores.local.file", "other", ""},
		"bare":          {"", "other", ""},
		"orders-in":     {"bindings.localstorage", "binding", "input"},
		"orders-out":    {"bindings.localstorage", "binding", "output"},
		"orders-inout":  {"bindings.localstorage", "binding", "input,output"},
		"orders-odd":    {"bindings.localstorage", "binding", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d components, want %d: %+v", len(got), len(want), got)
	}
	// Sorted by Name.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Errorf("components not sorted by name: %+v", got)
		}
	}
	for _, c := range got {
		w, ok := want[c.Name]
		if !ok {
			t.Errorf("unexpected component %q", c.Name)
			continue
		}
		if c.Type != w.typ || c.Category != w.cat || c.Direction != w.dir {
			t.Errorf("%q = {type:%q cat:%q dir:%q}, want {type:%q cat:%q dir:%q}",
				c.Name, c.Type, c.Category, c.Direction, w.typ, w.cat, w.dir)
		}
	}
}
