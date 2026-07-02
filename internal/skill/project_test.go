package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindProject(t *testing.T) {
	t.Run("found in current dir", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "skills.json"), []byte(`{"skills":[]}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}

		p, err := FindProject(tmp)
		if err != nil {
			t.Fatalf("FindProject: %v", err)
		}
		if p.Root != tmp {
			t.Errorf("Root: got %q, want %q", p.Root, tmp)
		}
	})

	t.Run("found in parent", func(t *testing.T) {
		tmp := t.TempDir()
		parent := filepath.Join(tmp, "parent")
		child := filepath.Join(parent, "child")
		if err := os.MkdirAll(child, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "skills.json"), []byte(`{"skills":[]}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}

		p, err := FindProject(child)
		if err != nil {
			t.Fatalf("FindProject: %v", err)
		}
		if p.Root != parent {
			t.Errorf("Root: got %q, want %q", p.Root, parent)
		}
	})

	t.Run("not found", func(t *testing.T) {
		tmp := t.TempDir()
		_, err := FindProject(tmp)
		if !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("expected ErrProjectNotFound, got: %v", err)
		}
	})
}

func TestProjectPaths(t *testing.T) {
	p := &Project{Root: "/project"}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"ManifestPath", p.ManifestPath(), "/project/skills.json"},
		{"LockfilePath", p.LockfilePath(), "/project/skills.lock.json"},
		{"SkillsDir", p.SkillsDir(), "/project/.agents/skills"},
		{"SkillDir", p.SkillDir("foo"), "/project/.agents/skills/foo"},
		{"SkillRelPath", p.SkillRelPath("foo"), ".agents/skills/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestProjectAdditionalPaths(t *testing.T) {
	p := &Project{Root: "/project"}
	entry := ManifestEntry{
		Name:                "foo",
		Source:              "ghcr.io/example/foo",
		AdditionalBasePaths: []string{"tools", "scripts"},
	}

	dirs := p.AdditionalDirs(entry)
	want := []string{"/project/tools/foo", "/project/scripts/foo"}
	if len(dirs) != len(want) {
		t.Fatalf("AdditionalDirs: got %v, want %v", dirs, want)
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("AdditionalDirs[%d]: got %q, want %q", i, d, want[i])
		}
	}

	rels := p.AdditionalRelPaths(entry)
	wantRels := []string{"tools/foo", "scripts/foo"}
	if len(rels) != len(wantRels) {
		t.Fatalf("AdditionalRelPaths: got %v, want %v", rels, wantRels)
	}
	for i, r := range rels {
		if r != wantRels[i] {
			t.Errorf("AdditionalRelPaths[%d]: got %q, want %q", i, r, wantRels[i])
		}
	}
}

func TestProjectManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	m := &Manifest{
		Skills: []ManifestEntry{
			{Name: "foo", Source: "ghcr.io/example/foo"},
		},
	}
	if err := p.SaveManifest(m); err != nil {
		t.Fatal(err)
	}

	loaded, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].Name != "foo" {
		t.Errorf("loaded mismatch: %+v", loaded.Skills)
	}
}

func TestProjectLockfileRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	l := &Lockfile{Skills: []LockEntry{{Name: "foo", Path: ".agents/skills/foo"}}}
	if err := p.SaveLockfile(l); err != nil {
		t.Fatal(err)
	}

	loaded, err := p.LoadLockfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].Name != "foo" {
		t.Errorf("loaded mismatch: %+v", loaded.Skills)
	}
}
