package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/skill"
)

func TestConfirmInstallSkills(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"yes", "y\n", true},
		{"no", "n\n", false},
		{"empty defaults to yes", "\n", true},
		{"eof declines", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			got, err := confirmInstallSkills(strings.NewReader(tc.input), out)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if !strings.Contains(out.String(), "[Y/n]") {
				t.Errorf("prompt %q missing [Y/n] hint", out.String())
			}
		})
	}
}

func TestSkillProjectAtBootstraps(t *testing.T) {
	tmp := t.TempDir()
	p, err := skillProjectAt(tmp)
	if err != nil {
		t.Fatalf("skillProjectAt: %v", err)
	}
	if p.Root != tmp {
		t.Errorf("root = %q, want %q", p.Root, tmp)
	}
	if _, err := os.Stat(filepath.Join(tmp, "skills.json")); err != nil {
		t.Errorf("expected skills.json to be created: %v", err)
	}
}

func TestSkillProjectAtDoesNotWalkUp(t *testing.T) {
	parent := t.TempDir()
	if err := (&skill.Project{Root: parent}).SaveManifest(&skill.Manifest{Skills: []skill.ManifestEntry{}}); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "new-integration")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := skillProjectAt(child)
	if err != nil {
		t.Fatalf("skillProjectAt: %v", err)
	}
	if p.Root != child {
		t.Errorf("root = %q, want %q (must not walk up to parent)", p.Root, child)
	}
	if _, err := os.Stat(filepath.Join(child, "skills.json")); err != nil {
		t.Errorf("expected skills.json in the new integration: %v", err)
	}
}

func TestSkillProjectAtKeepsExisting(t *testing.T) {
	tmp := t.TempDir()
	existing := &skill.Manifest{Skills: []skill.ManifestEntry{
		{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
	}}
	if err := (&skill.Project{Root: tmp}).SaveManifest(existing); err != nil {
		t.Fatal(err)
	}

	p, err := skillProjectAt(tmp)
	if err != nil {
		t.Fatalf("skillProjectAt: %v", err)
	}
	manifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].Name != "pr-review" {
		t.Errorf("existing manifest was clobbered: %+v", manifest.Skills)
	}
}

func TestMaybeInstallSkillsSkips(t *testing.T) {
	t.Run("no-input", func(t *testing.T) {
		errW := &bytes.Buffer{}
		if err := maybeInstallSkills(context.Background(), os.Stdin, errW, false, true, t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(errW.String(), "skills not installed") {
			t.Errorf("expected the install-later hint, got %q", errW.String())
		}
	})
	t.Run("non-terminal stdin", func(t *testing.T) {
		errW := &bytes.Buffer{}
		if err := maybeInstallSkills(context.Background(), strings.NewReader("y\n"), errW, false, false, t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(errW.String(), "intropy skills collection add") {
			t.Errorf("expected the install-later hint, got %q", errW.String())
		}
	})
}

func TestDecideInstallSkills(t *testing.T) {
	errW := &bytes.Buffer{}
	cases := []struct {
		name    string
		force   bool
		noInput bool
		in      io.Reader
		want    bool
	}{
		{"force wins over no-input", true, true, strings.NewReader(""), true},
		{"force wins over non-terminal stdin", true, false, strings.NewReader(""), true},
		{"no-input skips", false, true, os.Stdin, false},
		{"non-terminal stdin skips", false, false, strings.NewReader("y\n"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decideInstallSkills(tc.force, tc.noInput, tc.in, errW)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSkillsCollectionRef(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("INTROPY_SKILLS_COLLECTION", "")
		if got := skillsCollectionRef(); got != defaultSkillsCollectionRef {
			t.Errorf("got %q, want %q", got, defaultSkillsCollectionRef)
		}
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("INTROPY_SKILLS_COLLECTION", "localhost:5555/skills/index:latest")
		if got := skillsCollectionRef(); got != "localhost:5555/skills/index:latest" {
			t.Errorf("got %q", got)
		}
	})
}
