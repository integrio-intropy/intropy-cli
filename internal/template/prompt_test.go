package template

import (
	"bytes"
	"strings"
	"testing"
)

func promptRun(t *testing.T, input string, f FieldSpec) (any, bool, string, error) {
	t.Helper()
	var out bytes.Buffer
	p := NewStdinPrompter(strings.NewReader(input), &out)
	v, applied, err := p.Prompt(f)
	return v, applied, out.String(), err
}

func TestStdinPrompterSuggestions(t *testing.T) {
	field := func(suggestions ...string) FieldSpec {
		return FieldSpec{Name: "topic", Type: "string", Required: true, Suggestions: suggestions}
	}

	t.Run("single suggestion: Enter accepts and reports applied", func(t *testing.T) {
		v, applied, out, err := promptRun(t, "\n", field("orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "orders" {
			t.Errorf("value = %v", v)
		}
		if !applied {
			t.Error("expected the shown default to report applied")
		}
		if !strings.Contains(out, "topic [orders]: ") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("single suggestion: typing a new value applies nothing", func(t *testing.T) {
		v, applied, _, err := promptRun(t, "brand-new\n", field("orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "brand-new" {
			t.Errorf("value = %v", v)
		}
		if applied {
			t.Error("a fresh-typed value must not report applied")
		}
	})

	t.Run("several suggestions render as a numbered list with a free-text escape", func(t *testing.T) {
		_, _, out, err := promptRun(t, "2\n", field("audits", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "  1) audits\n  2) orders\n") {
			t.Errorf("output = %q", out)
		}
		if !strings.Contains(out, "or type a new value") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("several suggestions: a numbered pick applies the pick", func(t *testing.T) {
		v, applied, _, err := promptRun(t, "2\n", field("audits", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "orders" {
			t.Errorf("value = %v", v)
		}
		if !applied {
			t.Error("expected the shown default to report applied")
		}
	})

	t.Run("several suggestions: typing the candidate exactly applies it", func(t *testing.T) {
		v, applied, _, err := promptRun(t, "audits\n", field("audits", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "audits" {
			t.Errorf("value = %v", v)
		}
		if !applied {
			t.Error("expected an exact candidate match to report applied")
		}
	})

	t.Run("several suggestions: a novel value is accepted, not rejected", func(t *testing.T) {
		v, applied, _, err := promptRun(t, "brand-new\n", field("audits", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "brand-new" {
			t.Errorf("value = %v", v)
		}
		if applied {
			t.Error("a fresh-typed value must not report applied")
		}
	})

	t.Run("several suggestions: empty input re-prompts rather than applying", func(t *testing.T) {
		v, applied, out, err := promptRun(t, "\n1\n", field("audits", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		if v != "audits" {
			t.Errorf("value = %v", v)
		}
		if !applied {
			t.Error("expected the pick to report applied")
		}
		if !strings.Contains(out, "please choose one") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("novel value in a list is still pattern-validated", func(t *testing.T) {
		f := field("orders")
		f.Suggestions = []string{"audits", "orders"}
		f.Pattern = `^[a-z]+$`
		v, _, out, err := promptRun(t, "Not Valid\nvalid\n", f)
		if err != nil {
			t.Fatal(err)
		}
		if v != "valid" {
			t.Errorf("value = %v", v)
		}
		if !strings.Contains(out, "value must match") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("fields without suggestions render exactly as before", func(t *testing.T) {
		v, applied, out, err := promptRun(t, "typed\n", FieldSpec{Name: "topic", Type: "string"})
		if err != nil {
			t.Fatal(err)
		}
		if v != "typed" || applied {
			t.Errorf("value = %v, applied = %v", v, applied)
		}
		if out != "topic: " {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("enum fields ignore suggestions and report nothing applied", func(t *testing.T) {
		f := FieldSpec{Name: "kind", Type: "string", Enum: []any{"a", "b"}, Suggestions: []string{"a"}}
		v, applied, _, err := promptRun(t, "1\n", f)
		if err != nil {
			t.Fatal(err)
		}
		if v != "a" {
			t.Errorf("value = %v", v)
		}
		if applied {
			t.Error("a fresh-typed value must not report applied")
		}
	})
}
