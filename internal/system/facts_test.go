package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func TestWorkspaceFactsOf(t *testing.T) {
	t.Run("block records contribute, role records do not", func(t *testing.T) {
		block := template.ScaffoldEntry{
			Path: "order-extractor",
			Scaffold: template.Scaffold{
				Values:    map[string]any{"appId": "order-extractor", "topic": "orders", "contract": "Order"},
				BlockKind: template.BlockKindExtractor,
			},
		}
		lib := template.ScaffoldEntry{
			Path:     "Acme.Models",
			Scaffold: template.Scaffold{Values: map[string]any{"name": "Acme.Models"}, Role: template.RoleSharedLibrary},
		}
		facts := WorkspaceFactsOf([]template.ScaffoldEntry{block, lib})
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0].Name != "orders" {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})
}

func TestLoadWorkspaceFacts(t *testing.T) {
	writeRecord := func(t *testing.T, dir string, s template.Scaffold) {
		t.Helper()
		if err := template.WriteScaffold(dir, s); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("collects facts from records under root", func(t *testing.T) {
		root := t.TempDir()
		writeRecord(t, filepath.Join(root, "order-extractor"), template.Scaffold{
			SchemaVersion: template.ScaffoldSchemaVersion,
			Template:      "extractor",
			Version:       "v1.0.0",
			Values:        map[string]any{"appId": "order-extractor", "topic": "orders", "contract": "Order"},
			BlockKind:     template.BlockKindExtractor,
		})
		facts, warnings := LoadWorkspaceFacts(root)
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v", warnings)
		}
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0].Name != "orders" {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})

	t.Run("a malformed record warns without hiding the rest", func(t *testing.T) {
		root := t.TempDir()
		bad := filepath.Join(root, "bad", ".intropy")
		if err := os.MkdirAll(bad, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bad, "scaffold.json"), []byte("{nope"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeRecord(t, filepath.Join(root, "good"), template.Scaffold{
			SchemaVersion: template.ScaffoldSchemaVersion,
			Template:      "loader",
			Version:       "v1.0.0",
			Values:        map[string]any{"appId": "good", "topic": "audits", "contract": "Audit"},
			BlockKind:     template.BlockKindLoader,
		})
		facts, warnings := LoadWorkspaceFacts(root)
		if len(warnings) == 0 || !strings.Contains(warnings[0].Error(), "bad") {
			t.Fatalf("warnings = %v", warnings)
		}
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0].Name != "audits" {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})

	t.Run("missing root yields empty facts without fatal warnings", func(t *testing.T) {
		facts, _ := LoadWorkspaceFacts(filepath.Join(t.TempDir(), "nope"))
		if len(facts.TopicKeys) != 0 {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})
}
