package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		wantErr  bool
	}{
		{
			name: "valid",
			manifest: Manifest{
				Skills: []ManifestEntry{
					{Name: "pr-review", Source: "ghcr.io/example/pr-review"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing name",
			manifest: Manifest{
				Skills: []ManifestEntry{
					{Source: "ghcr.io/example/pr-review"},
				},
			},
			wantErr: true,
		},
		{
			name: "missing source",
			manifest: Manifest{
				Skills: []ManifestEntry{
					{Name: "pr-review"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate name",
			manifest: Manifest{
				Skills: []ManifestEntry{
					{Name: "pr-review", Source: "ghcr.io/example/pr-review"},
					{Name: "pr-review", Source: "ghcr.io/other/pr-review"},
				},
			},
			wantErr: true,
		},
		{
			name:     "empty manifest is valid",
			manifest: Manifest{Skills: []ManifestEntry{}},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills.json")

	original := &Manifest{
		Collections: []ManifestCollection{
			{Name: "intropy", Ref: "ghcr.io/intropy/skills/index:latest"},
		},
		Skills: []ManifestEntry{
			{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
		},
	}

	if err := SaveManifest(path, original); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if len(loaded.Collections) != 1 || loaded.Collections[0].Name != "intropy" {
		t.Errorf("collections mismatch: got %+v", loaded.Collections)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].Name != "pr-review" {
		t.Errorf("skills mismatch: got %+v", loaded.Skills)
	}
}

func TestLoadManifestNotFound(t *testing.T) {
	_, err := LoadManifest("/nonexistent/path/skills.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got: %v", err)
	}
}

func TestLoadManifestInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadManifestUnknownField(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills.json")
	content := `{"skills":[], "unknownField": true}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}
