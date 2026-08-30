package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func TestSeedOrganization(t *testing.T) {
	t.Run("config organization seeds an empty fact index", func(t *testing.T) {
		withConfig(t, "organization: integrio\n")
		facts := template.BuildWorkspaceFacts(nil)
		seedOrganization(facts)
		if got, ok := facts.Organization(); !ok || got != "integrio" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})

	t.Run("workspace records beat the config", func(t *testing.T) {
		withConfig(t, "organization: integrio\n")
		facts := template.BuildWorkspaceFacts([]template.WorkspaceFactEntry{
			{BlockKind: template.BlockKindExtractor, Values: map[string]any{"organization": "acme"}},
		})
		seedOrganization(facts)
		if got, ok := facts.Organization(); !ok || got != "acme" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})

	t.Run("no config file leaves the fact unset", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		facts := template.BuildWorkspaceFacts(nil)
		seedOrganization(facts)
		if got, ok := facts.Organization(); ok || got != "" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})
}

func TestResolveCreateName(t *testing.T) {
	t.Run("name only defaults output and sets name", func(t *testing.T) {
		sets := map[string]any{}
		out, err := resolveCreateName("orders", "", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "orders" {
			t.Errorf("output = %q, want %q", out, "orders")
		}
		if sets["name"] != "orders" {
			t.Errorf("sets[name] = %v, want %q", sets["name"], "orders")
		}
	})

	t.Run("pascal name defaults a kebab output", func(t *testing.T) {
		// The sys create convention: a name and its kebab form are one
		// component, so OrderSync and order-sync scaffold the same directory.
		sets := map[string]any{}
		out, err := resolveCreateName("OrderSync", "", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "order-sync" {
			t.Errorf("output = %q, want %q", out, "order-sync")
		}
		if sets["name"] != "OrderSync" {
			t.Errorf("sets[name] = %v, want the verbatim name", sets["name"])
		}
	})

	t.Run("explicit output is kept, name still set", func(t *testing.T) {
		// Mirrors `dotnet new`: -o is the literal output location; -n never
		// nests a subdirectory under it.
		sets := map[string]any{}
		out, err := resolveCreateName("orders", "./elsewhere", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "./elsewhere" {
			t.Errorf("output = %q, want %q", out, "./elsewhere")
		}
		if sets["name"] != "orders" {
			t.Errorf("sets[name] = %v, want %q", sets["name"], "orders")
		}
	})

	t.Run("name plus --set name conflict is a usage error", func(t *testing.T) {
		sets := map[string]any{"name": "bar"}
		_, err := resolveCreateName("foo", "", sets)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
		if sets["name"] != "bar" {
			t.Errorf("sets[name] mutated on conflict: %v", sets["name"])
		}
	})

	t.Run("no name is a passthrough", func(t *testing.T) {
		sets := map[string]any{}
		out, err := resolveCreateName("", "./out", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "./out" {
			t.Errorf("output = %q, want %q", out, "./out")
		}
		if _, ok := sets["name"]; ok {
			t.Errorf("sets should be untouched, got %v", sets)
		}
	})
}

// outDirLibraryServer fakes the GitHub endpoints deriveOutDir's fetch
// calls: the release lookup and a tarball holding one template whose
// manifest declares the given extra parameter block.
func outDirLibraryServer(t *testing.T, extraParams string) *httptest.Server {
	t.Helper()
	manifest := `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: hello-world
spec:
  parameters:
    type: object
    required: [integrationName]
    properties:
      integrationName:
        type: string
`
	manifest += extraParams
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"o-r-abc/hello-world/template.yaml":           manifest,
		"o-r-abc/hello-world/skeleton/README.md.tmpl": "{{ .integrationName }}\n",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/v1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func deriveOutDirOpts(srv *httptest.Server, sets map[string]any) template.CreateOptions {
	var stderr bytes.Buffer
	return template.CreateOptions{
		Template:      "hello-world",
		SetValues:     sets,
		NoInput:       true,
		Stderr:        &stderr,
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	}
}

func TestDeriveOutDir(t *testing.T) {
	nameParam := "      name:\n        type: string\n"

	t.Run("resolved name kebab-cases into the directory", func(t *testing.T) {
		srv := outDirLibraryServer(t, nameParam)
		opts := deriveOutDirOpts(srv, map[string]any{"integrationName": "x", "name": "OrderSync"})
		out, err := deriveOutDir(context.Background(), opts)
		if err != nil {
			t.Fatalf("deriveOutDir: %v", err)
		}
		if out != "order-sync" {
			t.Errorf("out = %q, want order-sync", out)
		}
	})

	t.Run("no name parameter is a usage error", func(t *testing.T) {
		srv := outDirLibraryServer(t, "")
		_, err := deriveOutDir(context.Background(), deriveOutDirOpts(srv, map[string]any{"integrationName": "x"}))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
	})
}

func TestIntCreateOutputValidation(t *testing.T) {
	resetCreateFlags := func(t *testing.T) {
		t.Helper()
		intCreateFlags = createFlags{}
		t.Cleanup(func() { intCreateFlags = createFlags{} })
	}

	t.Run("non-json --output is a usage error", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--output", "./out", "--name", "x", "--no-input"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid output format") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestWorkspaceRootOf(t *testing.T) {
	t.Run("bare name scans the working directory", func(t *testing.T) {
		if got := workspaceRootOf("orders-api"); got != "." {
			t.Errorf("workspaceRootOf = %q, want .", got)
		}
	})
	t.Run("nested output scans its parent", func(t *testing.T) {
		if got := workspaceRootOf("systems/acme/orders-api"); got != "systems/acme" {
			t.Errorf("workspaceRootOf = %q, want systems/acme", got)
		}
	})
	t.Run("empty scans the working directory", func(t *testing.T) {
		if got := workspaceRootOf(""); got != "." {
			t.Errorf("workspaceRootOf = %q, want .", got)
		}
	})
}
