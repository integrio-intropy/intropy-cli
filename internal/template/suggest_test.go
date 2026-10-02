package template

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func suggestFacts() *WorkspaceFacts {
	return BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			KeyPublishes: "order-created", KeyPort: "erp", KeyOrganization: "acme",
		}},
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			KeyPublishes: "audit-created", KeyPort: "warehouse",
		}},
	})
}

func reqField(name string) FieldSpec {
	return FieldSpec{Name: name, Type: "string", Required: true}
}

func TestSuggestMessageParameters(t *testing.T) {
	facts := suggestFacts()
	facts.SetMessageParameters([]string{KeySubscribes})
	got := Suggest([]FieldSpec{reqField(KeySubscribes), reqField(KeyPort)}, facts, nil)
	if !reflect.DeepEqual(got[KeySubscribes], []string{"audit-created", "order-created"}) {
		t.Fatalf("subscribes suggestions = %v", got[KeySubscribes])
	}
	if len(got[KeyPort]) != 0 {
		t.Fatalf("port suggestions = %v, want none", got[KeyPort])
	}
}

func TestSuggestOrganization(t *testing.T) {
	got := Suggest([]FieldSpec{reqField(KeyOrganization)}, suggestFacts(), nil)
	if !reflect.DeepEqual(got[KeyOrganization], []string{"acme"}) {
		t.Fatalf("organization = %v", got[KeyOrganization])
	}
}

func TestResolveWithMessagePrefill(t *testing.T) {
	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{{BlockKind: BlockKindExtractor, Values: map[string]any{KeyPublishes: "order-created"}}})
	facts.SetMessageParameters([]string{KeySubscribes})
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{KeySubscribes},
		"properties": map[string]any{
			KeySubscribes: map[string]any{"type": "string"},
		},
	}, []string{KeySubscribes}, nil)

	var notes bytes.Buffer
	p := &fakePrompter{answers: map[string]any{}}
	out, err := ResolveWith(tmpl, ResolveOptions{Facts: facts, Prompter: p, Notes: &notes})
	if err != nil {
		t.Fatal(err)
	}
	if out[KeySubscribes] != "order-created" {
		t.Fatalf("values = %v", out)
	}
	if len(p.seen) != 0 {
		t.Fatalf("no prompts expected, got %v", p.seen)
	}
	if !strings.Contains(notes.String(), "subscribes: order-created") {
		t.Fatalf("notes = %q", notes.String())
	}
}

func TestResolveWithMissingMessageNamesKnownValues(t *testing.T) {
	facts := suggestFacts()
	facts.SetMessageParameters([]string{KeySubscribes})
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{KeySubscribes},
		"properties": map[string]any{
			KeySubscribes: map[string]any{"type": "string"},
		},
	}, []string{KeySubscribes}, nil)
	_, err := ResolveWith(tmpl, ResolveOptions{Facts: facts})
	if err == nil || !strings.Contains(err.Error(), "pass --subscribe") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveWithZeroOptionsMatchesResolve(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}, []string{"name"}, nil)
	_, err := ResolveWith(tmpl, ResolveOptions{})
	if err == nil || err.Error() != "missing required parameter(s): name" {
		t.Fatalf("err = %v", err)
	}
}
