package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/config"
)

// withConfig writes content as the user's config.yaml under a temporary
// XDG_CONFIG_HOME and clears the env override so the file is the only
// source unless a test sets it back.
func withConfig(t *testing.T, content string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv(config.EnvTemplateRepo, "")
	dir := filepath.Join(base, "intropy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTemplateRepo(t *testing.T) {
	t.Run("nothing configured yields the default library", func(t *testing.T) {
		// No config file at all: an empty XDG_CONFIG_HOME with no intropy dir.
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv(config.EnvTemplateRepo, "")
		owner, repo, err := resolveTemplateRepo("")
		if err != nil || owner != "" || repo != "" {
			t.Errorf("resolveTemplateRepo = %q, %q, %v; want empty (default library)", owner, repo, err)
		}
	})

	t.Run("config file", func(t *testing.T) {
		withConfig(t, "templateRepo: acme/intropy-templates\n")
		owner, repo, err := resolveTemplateRepo("")
		if err != nil {
			t.Fatal(err)
		}
		if owner != "acme" || repo != "intropy-templates" {
			t.Errorf("resolveTemplateRepo = %q, %q", owner, repo)
		}
	})

	t.Run("env beats file", func(t *testing.T) {
		withConfig(t, "templateRepo: from-file/lib\n")
		t.Setenv(config.EnvTemplateRepo, "from-env/lib")
		owner, _, err := resolveTemplateRepo("")
		if err != nil {
			t.Fatal(err)
		}
		if owner != "from-env" {
			t.Errorf("owner = %q, want from-env", owner)
		}
	})

	t.Run("flag beats env", func(t *testing.T) {
		withConfig(t, "")
		t.Setenv(config.EnvTemplateRepo, "from-env/lib")
		owner, _, err := resolveTemplateRepo("from-flag/lib")
		if err != nil {
			t.Fatal(err)
		}
		if owner != "from-flag" {
			t.Errorf("owner = %q, want from-flag", owner)
		}
	})

	t.Run("malformed value in the file is an error, not a fallback", func(t *testing.T) {
		withConfig(t, "templateRepo: https://github.com/acme/lib\n")
		_, _, err := resolveTemplateRepo("")
		if err == nil {
			t.Fatal("a URL in templateRepo should fail")
		}
		if !strings.Contains(err.Error(), "owner/repo") {
			t.Errorf("error %q should name the expected format", err)
		}
	})
}
