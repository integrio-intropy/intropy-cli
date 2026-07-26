package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withConfigDir points Dir at a temporary directory and returns the path the
// config file would occupy.
func withConfigDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	return filepath.Join(base, dirName, fileName)
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/somewhere/cfg")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/somewhere/cfg", "intropy"); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}

// On macOS os.UserConfigDir would return ~/Library/Application Support, which
// is not where anyone looks for a CLI's config and not where argocd keeps its
// own. Everything must land under ~/.config.
func TestDirFallsBackToDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "intropy"); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	withConfigDir(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() on a missing file should succeed, got %v", err)
	}
	if cfg != (Config{}) {
		t.Errorf("Load() = %+v, want zero Config", cfg)
	}
}

func TestLoadEmptyFileIsNotAnError(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() on an empty file should succeed, got %v", err)
	}
	if cfg != (Config{}) {
		t.Errorf("Load() = %+v, want zero Config", cfg)
	}
}

func TestLoadReadsSettings(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "gitopsRepo: git@gitlab.com:acme/gitops.git\nargocdServer: argocd.example.com\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitopsRepo != "git@gitlab.com:acme/gitops.git" {
		t.Errorf("GitopsRepo = %q", cfg.GitopsRepo)
	}
	if cfg.ArgocdServer != "argocd.example.com" {
		t.Errorf("ArgocdServer = %q", cfg.ArgocdServer)
	}
}

// A misspelled key that is silently dropped looks exactly like a setting the
// user never wrote, and surfaces much later as "no GitOps repository
// configured" while the file plainly contains one.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "gitopsRepos: git@gitlab.com:acme/gitops.git\n")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject an unknown key")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the file, got %q", err)
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "gitopsRepo: [unclosed\n")
	if _, err := Load(); err == nil {
		t.Fatal("Load() should reject malformed YAML")
	}
}

func TestResolvePrecedence(t *testing.T) {
	file := Config{GitopsRepo: "from-file", ArgocdServer: "argo-from-file"}

	cases := []struct {
		name       string
		flags      Flags
		env        map[string]string
		wantRepo   string
		wantArgocd string
	}{
		{
			name:       "file only",
			wantRepo:   "from-file",
			wantArgocd: "argo-from-file",
		},
		{
			name:       "env beats file",
			env:        map[string]string{EnvGitopsRepo: "from-env", EnvArgocdServer: "argo-from-env"},
			wantRepo:   "from-env",
			wantArgocd: "argo-from-env",
		},
		{
			name:       "flag beats env",
			flags:      Flags{GitopsRepo: "from-flag", ArgocdServer: "argo-from-flag"},
			env:        map[string]string{EnvGitopsRepo: "from-env", EnvArgocdServer: "argo-from-env"},
			wantRepo:   "from-flag",
			wantArgocd: "argo-from-flag",
		},
		{
			name:       "settings layer independently",
			flags:      Flags{GitopsRepo: "from-flag"},
			env:        map[string]string{EnvArgocdServer: "argo-from-env"},
			wantRepo:   "from-flag",
			wantArgocd: "argo-from-env",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvGitopsRepo, "")
			t.Setenv(EnvArgocdServer, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := file.Resolve(tc.flags)
			if got.GitopsRepo != tc.wantRepo {
				t.Errorf("GitopsRepo = %q, want %q", got.GitopsRepo, tc.wantRepo)
			}
			if got.ArgocdServer != tc.wantArgocd {
				t.Errorf("ArgocdServer = %q, want %q", got.ArgocdServer, tc.wantArgocd)
			}
		})
	}
}

func TestRequireGitopsRepo(t *testing.T) {
	if _, err := (Config{GitopsRepo: "x"}).RequireGitopsRepo(); err != nil {
		t.Errorf("RequireGitopsRepo() with a value should succeed, got %v", err)
	}

	withConfigDir(t)
	_, err := Config{}.RequireGitopsRepo()
	if err == nil {
		t.Fatal("RequireGitopsRepo() with no value should fail")
	}
	// The message has to tell the user all three ways to supply it, or they
	// are left guessing which one this CLI wants.
	for _, want := range []string{"--gitops-repo", EnvGitopsRepo, "gitopsRepo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}
