package main

import (
	"errors"
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
