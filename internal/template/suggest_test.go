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
			"topic": "orders", "contract": "Order", "pubsub": "pubsub", "port": "erp",
		}},
		{BlockKind: BlockKindLoader, Values: map[string]any{
			"topic": "audits", "contract": "Audit", "pubsub": "pubsub", "port": "warehouse",
		}},
		{BlockKind: BlockKindTransactional, Values: map[string]any{
			"fromPort": "crm", "toPort": "erp",
		}},
	})
}

func reqField(name string) FieldSpec {
	return FieldSpec{Name: name, Type: "string", Required: true}
}

func TestSuggest(t *testing.T) {
	facts := suggestFacts()

	t.Run("topic and pubsub come from known topic keys", func(t *testing.T) {
		fields := []FieldSpec{reqField("topic"), reqField("pubsub")}
		got := Suggest(fields, facts, nil)
		if !reflect.DeepEqual(got["topic"], []string{"audits", "orders"}) {
			t.Errorf("topic = %v", got["topic"])
		}
		if !reflect.DeepEqual(got["pubsub"], []string{"pubsub"}) {
			t.Errorf("pubsub = %v", got["pubsub"])
		}
	})

	t.Run("ports are anti-suggestions: every block needs its own", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("port"), reqField("fromPort"), reqField("toPort")}, facts, nil)
		if len(got) != 0 {
			t.Errorf("suggestions = %v", got)
		}
	})

	t.Run("contract chains off a confirmed topic", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("contract")}, facts, map[string]any{"topic": "orders"})
		if !reflect.DeepEqual(got["contract"], []string{"Order"}) {
			t.Errorf("contract = %v", got["contract"])
		}
	})

	t.Run("contract without a confirmed topic lists every known contract", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("contract")}, facts, nil)
		if !reflect.DeepEqual(got["contract"], []string{"Audit", "Order"}) {
			t.Errorf("contract = %v", got["contract"])
		}
	})

	t.Run("confirmed topic unknown to the facts yields no contract suggestion", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("contract")}, facts, map[string]any{"topic": "brand-new"})
		if len(got["contract"]) != 0 {
			t.Errorf("contract = %v", got["contract"])
		}
	})

	t.Run("unregistered parameter names get no suggestions", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("name"), reqField("appId")}, facts, nil)
		if len(got) != 0 {
			t.Errorf("suggestions = %v", got)
		}
	})

	t.Run("a candidate equal to the schema default is dropped", func(t *testing.T) {
		f := reqField("pubsub")
		f.Default = "pubsub"
		got := Suggest([]FieldSpec{f}, facts, nil)
		if len(got["pubsub"]) != 0 {
			t.Errorf("pubsub = %v", got["pubsub"])
		}
	})

	t.Run("conflicted contract for the confirmed topic yields nothing", func(t *testing.T) {
		conflicted := BuildWorkspaceFacts([]WorkspaceFactEntry{
			{BlockKind: BlockKindExtractor, Values: map[string]any{"topic": "orders", "contract": "Order"}},
			{BlockKind: BlockKindLoader, Values: map[string]any{"topic": "orders", "contract": "OrderV2"}},
		})
		got := Suggest([]FieldSpec{reqField("contract")}, conflicted, map[string]any{"topic": "orders"})
		if len(got["contract"]) != 0 {
			t.Errorf("contract = %v", got["contract"])
		}
	})

	t.Run("nil facts yield no suggestions", func(t *testing.T) {
		got := Suggest([]FieldSpec{reqField("topic")}, nil, nil)
		if got != nil {
			t.Errorf("suggestions = %v", got)
		}
	})
}

func TestResolveWithPrefill(t *testing.T) {
	oneTopic := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			"topic": "orders", "contract": "Order",
		}},
	})
	twoParams := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"topic", "contract"},
		"properties": map[string]any{
			"topic":    map[string]any{"type": "string"},
			"contract": map[string]any{"type": "string"},
		},
	}, []string{"topic", "contract"}, nil)

	t.Run("single-candidate wiring resolves without prompting", func(t *testing.T) {
		var notes bytes.Buffer
		p := &fakePrompter{answers: map[string]any{}}
		out, err := ResolveWith(twoParams, ResolveOptions{Facts: oneTopic, Prompter: p, Notes: &notes})
		if err != nil {
			t.Fatal(err)
		}
		if out["topic"] != "orders" || out["contract"] != "Order" {
			t.Fatalf("values = %v", out)
		}
		if len(p.seen) != 0 {
			t.Fatalf("no prompts expected, got %v", p.seen)
		}
		for _, want := range []string{
			"topic: orders (from workspace; override with --set topic=<value>)",
			"contract: Order (from workspace; override with --set contract=<value>)",
		} {
			if !strings.Contains(notes.String(), want) {
				t.Errorf("notes missing %q: %q", want, notes.String())
			}
		}
	})

	t.Run("--set beats the prefill", func(t *testing.T) {
		p := &fakePrompter{answers: map[string]any{"contract": "Shipment"}}
		out, err := ResolveWith(twoParams, ResolveOptions{Facts: oneTopic, Sets: map[string]any{"topic": "shipments"}, Prompter: p})
		if err != nil {
			t.Fatal(err)
		}
		if out["topic"] != "shipments" {
			t.Errorf("topic = %v", out["topic"])
		}
		// A --set topic unknown to the facts leaves contract unprefilled;
		// the prompt answers it.
		if out["contract"] != "Shipment" {
			t.Errorf("contract = %v", out["contract"])
		}
	})

	t.Run("several candidates still prompt with the pick list", func(t *testing.T) {
		p := &fakePrompter{
			answers: map[string]any{"topic": "orders", "contract": "Order"},
		}
		out, err := ResolveWith(twoParams, ResolveOptions{Facts: suggestFacts(), Prompter: p})
		if err != nil {
			t.Fatal(err)
		}
		if out["topic"] != "orders" || out["contract"] != "Order" {
			t.Fatalf("values = %v", out)
		}
		if len(p.seen) != 2 {
			t.Fatalf("prompts = %d", len(p.seen))
		}
		// The first prompt offers every known topic; the second, chained
		// off the applied topic pick, offers only that topic's contract.
		if !reflect.DeepEqual(p.seen[0].Suggestions, []string{"audits", "orders"}) {
			t.Errorf("topic suggestions = %v", p.seen[0].Suggestions)
		}
		if !reflect.DeepEqual(p.seen[1].Suggestions, []string{"Order"}) {
			t.Errorf("contract suggestions = %v", p.seen[1].Suggestions)
		}
	})
}

func TestResolveWithMissingErrorNamesKnownValues(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"topic"},
		"properties": map[string]any{
			"topic": map[string]any{"type": "string"},
		},
	}, []string{"topic"}, nil)

	_, err := ResolveWith(tmpl, ResolveOptions{Facts: suggestFacts()})
	if err == nil {
		t.Fatal("expected error")
	}
	want := "missing required parameter(s): topic\nknown topic values in this workspace: audits, orders"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestResolveWithZeroOptionsMatchesResolve(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"topic"},
		"properties": map[string]any{
			"topic": map[string]any{"type": "string"},
		},
	}, []string{"topic"}, nil)

	_, err := ResolveWith(tmpl, ResolveOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "missing required parameter(s): topic" {
		t.Errorf("error = %q", err.Error())
	}
}

// TestResolveWithTypedTopicChainsContract pins the prompt loop's
// re-derivation invariant: a topic typed fresh (not picked from the
// candidate list) still constrains the contract prompt to that topic's
// recorded contract, because candidates re-derive from the value map
// after every answer.
func TestResolveWithTypedTopicChainsContract(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"topic", "contract"},
		"properties": map[string]any{
			"topic":    map[string]any{"type": "string"},
			"contract": map[string]any{"type": "string"},
		},
	}, []string{"topic", "contract"}, nil)

	// Two topics make topic a prompt (no prefill); "orders" is one of the
	// candidates, but the fake types it as an answer rather than picking.
	p := &fakePrompter{
		answers: map[string]any{"topic": "orders", "contract": "Order"},
	}
	out, err := ResolveWith(tmpl, ResolveOptions{Facts: suggestFacts(), Prompter: p})
	if err != nil {
		t.Fatal(err)
	}
	if out["topic"] != "orders" || out["contract"] != "Order" {
		t.Fatalf("values = %v", out)
	}
	if len(p.seen) != 2 {
		t.Fatalf("prompts = %d", len(p.seen))
	}
	if !reflect.DeepEqual(p.seen[1].Suggestions, []string{"Order"}) {
		t.Errorf("contract suggestions = %v", p.seen[1].Suggestions)
	}
}
