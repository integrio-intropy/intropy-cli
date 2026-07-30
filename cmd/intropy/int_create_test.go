package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

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

func TestIntCreateDeprecatedOutputAlias(t *testing.T) {
	resetCreateFlags := func(t *testing.T) {
		t.Helper()
		intCreateFlags = createFlags{}
		t.Cleanup(func() { intCreateFlags = createFlags{} })
	}

	t.Run("alias alone satisfies the required group and warns", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--output", "./out", "--name", "x", "--no-input"})
		err := rootCmd.Execute()
		// We expect a template-fetch error (no network), but NOT a usage error
		// about missing required flags.
		if err != nil {
			var ue *usageError
			if errors.As(err, &ue) && strings.Contains(err.Error(), "required") {
				t.Errorf("deprecated --output should satisfy the required group, got usage error: %v", err)
			}
		}
		if !strings.Contains(stderr.String(), "deprecated") {
			t.Errorf("expected deprecation warning on stderr, got: %q", stderr.String())
		}
		if intCreateFlags.outDir != "./out" {
			t.Errorf("outDir = %q, want alias copy %q", intCreateFlags.outDir, "./out")
		}
	})

	t.Run("alias and --out-dir together are a usage error", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--output", "./a", "--out-dir", "./b", "--name", "x"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestIntCreateOutputJSONFormat(t *testing.T) {
	resetCreateFlags := func(t *testing.T) {
		t.Helper()
		intCreateFlags = createFlags{}
		t.Cleanup(func() { intCreateFlags = createFlags{} })
	}

	t.Run("--output json maps to outputJSON stdout", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--name", "x", "--output", "json", "--no-input"})
		_ = rootCmd.Execute() // network error expected; flag wiring is what we assert
		if intCreateFlags.outputJSON != "-" {
			t.Errorf("outputJSON = %q, want %q (--output json maps to stdout)", intCreateFlags.outputJSON, "-")
		}
		if intCreateFlags.outDir != "" {
			t.Errorf("outDir = %q, want empty (--output json is not a directory)", intCreateFlags.outDir)
		}
	})

	t.Run("--output json with --output-json path is a usage error", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--name", "x", "--output", "json", "--output-json", "result.json"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("--output-json - alone warns about deprecation", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--name", "x", "--output-json", "-", "--no-input"})
		_ = rootCmd.Execute() // network error expected
		if !strings.Contains(stderr.String(), "--output-json - is deprecated") {
			t.Errorf("expected deprecation warning for --output-json -, got: %q", stderr.String())
		}
	})
}
