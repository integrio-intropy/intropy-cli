package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(cfg, Config{}) {
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
	if !reflect.DeepEqual(cfg, Config{}) {
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

func TestLoadReadsContexts(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `organization: integrio
currentContext: acme
contexts:
  acme:
    organization: acme
    gitopsRepo: git@gitlab.com:acme/gitops.git
  staging-eu:
    gitopsRepo: git@gitlab.com:staging-eu/gitops.git
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Organization != "integrio" {
		t.Errorf("Organization = %q", cfg.Organization)
	}
	if cfg.CurrentContext != "acme" {
		t.Errorf("CurrentContext = %q", cfg.CurrentContext)
	}
	if len(cfg.Contexts) != 2 {
		t.Fatalf("Contexts has %d entries, want 2", len(cfg.Contexts))
	}
	acme := cfg.Contexts["acme"]
	if acme.Organization != "acme" || acme.GitopsRepo != "git@gitlab.com:acme/gitops.git" {
		t.Errorf("Contexts[acme] = %+v", acme)
	}
	eu := cfg.Contexts["staging-eu"]
	if eu.Organization != "" || eu.GitopsRepo != "git@gitlab.com:staging-eu/gitops.git" {
		t.Errorf("Contexts[staging-eu] = %+v", eu)
	}
}

// A currentContext pointing at nothing is the same class of error as a
// typo'd key: the file's owner hears about it at load, from every command.
func TestLoadRejectsUnknownCurrentContext(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "currentContext: acmee\ncontexts:\n  acme: {}\n  integrio: {}\n")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject a dangling currentContext")
	}
	for _, want := range []string{"acmee", path, "acme, integrio"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestLoadRejectsCurrentContextWithoutContexts(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "currentContext: acme\n")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject currentContext with no contexts")
	}
	if !strings.Contains(err.Error(), "no contexts configured") {
		t.Errorf("error %q should say no contexts are configured", err)
	}
}

func TestLoadRejectsCurrentContextWithEmptyContexts(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "currentContext: acme\ncontexts: {}\n")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject currentContext with empty contexts")
	}
	if !strings.Contains(err.Error(), "no contexts configured") {
		t.Errorf("error %q should say no contexts are configured", err)
	}
}

func TestLoadRejectsUnknownKeyInsideContext(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, "contexts:\n  acme:\n    gitoopsRepo: git@gitlab.com:acme/gitops.git\n")
	if _, err := Load(); err == nil {
		t.Fatal("Load() should reject an unknown key inside a context")
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

func TestResolveContextRung(t *testing.T) {
	file := Config{
		GitopsRepo:     "top-level-repo",
		TemplateRepo:   "top-level/library",
		Organization:   "top-level-org",
		CurrentContext: "acme",
		Contexts: map[string]Context{
			"acme": {GitopsRepo: "ctx-repo", Organization: "ctx-org"},
		},
	}

	// Clearing rather than unsetting: cmp.Or treats "" as absent, and CI
	// environments may export the borrowed ARGOCD_SERVER.
	t.Setenv(EnvGitopsRepo, "")
	t.Setenv(EnvArgocdServer, "")
	t.Setenv(EnvTemplateRepo, "")

	got := file.Resolve(Flags{})
	if got.GitopsRepo != "ctx-repo" {
		t.Errorf("GitopsRepo = %q, want the context value", got.GitopsRepo)
	}
	if got.TemplateRepo != "top-level/library" {
		t.Errorf("TemplateRepo = %q, want the top-level default to fall through", got.TemplateRepo)
	}
	if got.Organization != "ctx-org" {
		t.Errorf("Organization = %q, want the context value", got.Organization)
	}

	t.Run("env beats context", func(t *testing.T) {
		t.Setenv(EnvGitopsRepo, "env-repo")
		if got := file.Resolve(Flags{}).GitopsRepo; got != "env-repo" {
			t.Errorf("GitopsRepo = %q, want the env value", got)
		}
	})
	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv(EnvGitopsRepo, "env-repo")
		got := file.Resolve(Flags{GitopsRepo: "flag-repo"}).GitopsRepo
		if got != "flag-repo" {
			t.Errorf("GitopsRepo = %q, want the flag value", got)
		}
	})
	t.Run("flag beats context for organization", func(t *testing.T) {
		got := file.Resolve(Flags{Organization: "flag-org"}).Organization
		if got != "flag-org" {
			t.Errorf("Organization = %q, want the flag value", got)
		}
	})
}

// A config with no currentContext resolves exactly as before contexts
// existed: flag > env > top-level.
func TestResolveWithoutContextUnchanged(t *testing.T) {
	file := Config{GitopsRepo: "top-level-repo", Organization: "top-level-org"}
	t.Setenv(EnvGitopsRepo, "")
	got := file.Resolve(Flags{})
	if got.GitopsRepo != "top-level-repo" || got.Organization != "top-level-org" {
		t.Errorf("Resolve() = %+v, want the top-level values", got)
	}
}

func TestResolveTemplateRepoPrecedence(t *testing.T) {
	file := Config{TemplateRepo: "from-file/library"}

	t.Run("file only", func(t *testing.T) {
		t.Setenv(EnvTemplateRepo, "")
		if got := file.Resolve(Flags{}).TemplateRepo; got != "from-file/library" {
			t.Errorf("TemplateRepo = %q, want from-file/library", got)
		}
	})
	t.Run("env beats file", func(t *testing.T) {
		t.Setenv(EnvTemplateRepo, "from-env/library")
		if got := file.Resolve(Flags{}).TemplateRepo; got != "from-env/library" {
			t.Errorf("TemplateRepo = %q, want from-env/library", got)
		}
	})
	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv(EnvTemplateRepo, "from-env/library")
		got := file.Resolve(Flags{TemplateRepo: "from-flag/library"}).TemplateRepo
		if got != "from-flag/library" {
			t.Errorf("TemplateRepo = %q, want from-flag/library", got)
		}
	})
}

func TestParseTemplateRepo(t *testing.T) {
	t.Run("empty means the official library", func(t *testing.T) {
		owner, repo, err := ParseTemplateRepo("")
		if err != nil || owner != "" || repo != "" {
			t.Errorf("ParseTemplateRepo(\"\") = %q, %q, %v", owner, repo, err)
		}
	})
	t.Run("owner/repo splits", func(t *testing.T) {
		owner, repo, err := ParseTemplateRepo("acme/intropy-templates")
		if err != nil || owner != "acme" || repo != "intropy-templates" {
			t.Errorf("ParseTemplateRepo = %q, %q, %v", owner, repo, err)
		}
	})

	for _, bad := range []string{
		"acme",                      // no slash
		"acme/x/y",                  // too many segments
		"/x",                        // empty owner
		"x/",                        // empty repo
		"https://github.com/acme/x", // URL
		"git@github.com:acme/x.git", // SSH remote
	} {
		t.Run("rejects "+bad, func(t *testing.T) {
			if _, _, err := ParseTemplateRepo(bad); err == nil {
				t.Errorf("ParseTemplateRepo(%q) should fail", bad)
			} else if !strings.Contains(err.Error(), "owner/repo") {
				t.Errorf("error %q should name the expected format", err)
			}
		})
	}
}

func TestSetCurrentContext(t *testing.T) {
	t.Run("switches the active context", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "currentContext: integrio\ncontexts:\n  acme:\n    gitopsRepo: ctx-repo\n  integrio: {}\n")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Resolve(Flags{}).GitopsRepo; got != "ctx-repo" {
			t.Errorf("resolved GitopsRepo = %q, want the acme context value", got)
		}
	})

	t.Run("only the currentContext line changes", func(t *testing.T) {
		path := withConfigDir(t)
		original := "# my customer config\norganization: integrio # the default\ncurrentContext: integrio\ncontexts:\n  # the big customer\n  acme: {}\n  integrio: {}\n"
		writeConfig(t, path, original)
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(original, "currentContext: integrio", "currentContext: acme", 1)
		if string(raw) != want {
			t.Errorf("file = %q, want %q", raw, want)
		}
	})

	t.Run("appends when the key is absent", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "contexts:\n  acme: {}\n")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.HasSuffix(string(raw), "currentContext: acme\n") {
			t.Errorf("file = %q, want the key appended", raw)
		}
		if _, err := Load(); err != nil {
			t.Errorf("result should parse, got %v", err)
		}
	})

	t.Run("preserves a trailing comment", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "currentContext: integrio # the prod customer\ncontexts:\n  acme: {}\n  integrio: {}\n")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "currentContext: acme # the prod customer") {
			t.Errorf("file = %q, want the comment preserved", raw)
		}
	})

	t.Run("a quoted value is replaced wholesale", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "currentContext: \"integrio\"\ncontexts:\n  acme: {}\n  integrio: {}\n")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "currentContext: acme\n") || strings.Contains(string(raw), "\"") {
			t.Errorf("file = %q, want a plain scalar and no leftover quotes", raw)
		}
	})

	t.Run("a file without trailing newline is repaired first", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "contexts:\n  acme: {}")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "acme: {}\ncurrentContext: acme\n") {
			t.Errorf("file = %q, want the key on its own line", raw)
		}
	})

	t.Run("an indented look-alike is not the top-level key", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "contexts:\n  acme:\n    organization: \"currentContext: nested\"\n")
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "    organization: \"currentContext: nested\"") {
			t.Errorf("file = %q, want the nested line untouched", raw)
		}
		if !strings.HasSuffix(string(raw), "currentContext: acme\n") {
			t.Errorf("file = %q, want a new top-level key appended", raw)
		}
	})

	t.Run("unknown context fails without touching the file", func(t *testing.T) {
		path := withConfigDir(t)
		original := "contexts:\n  acme: {}\n"
		writeConfig(t, path, original)
		err := SetCurrentContext(path, "acmee")
		if err == nil {
			t.Fatal("SetCurrentContext should reject an unknown context")
		}
		if !strings.Contains(err.Error(), "acmee") || !strings.Contains(err.Error(), "acme") {
			t.Errorf("error %q should name the bad name and the valid ones", err)
		}
		raw, _ := os.ReadFile(path)
		if string(raw) != original {
			t.Errorf("file changed despite the error: %q", raw)
		}
	})

	t.Run("missing file is an error, nothing created", func(t *testing.T) {
		path := withConfigDir(t)
		if err := SetCurrentContext(path, "acme"); err == nil {
			t.Fatal("SetCurrentContext on a missing file should fail")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the file should not have been created")
		}
	})

	t.Run("a dangling currentContext blocks the write", func(t *testing.T) {
		path := withConfigDir(t)
		original := "currentContext: ghost\ncontexts:\n  acme: {}\n"
		writeConfig(t, path, original)
		if err := SetCurrentContext(path, "acme"); err == nil {
			t.Fatal("SetCurrentContext should refuse a file that fails validation")
		}
		raw, _ := os.ReadFile(path)
		if string(raw) != original {
			t.Errorf("file changed despite the error: %q", raw)
		}
	})

	t.Run("file mode is preserved", func(t *testing.T) {
		path := withConfigDir(t)
		writeConfig(t, path, "currentContext: integrio\ncontexts:\n  acme: {}\n  integrio: {}\n")
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SetCurrentContext(path, "acme"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 600", info.Mode().Perm())
		}
	})
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
