package template

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const routedManifest = `
apiVersion: intropy.io/v1
kind: Template
metadata:
  name: routed-loader
  labels:
    intropy.io/message-params: routes
spec:
  parameters:
    type: object
    required: [routes]
    properties:
      routes:
        type: array
        title: Routes
        minItems: 1
        items:
          type: object
          required: [message]
          properties:
            when:
              type: string
              title: Filter
            message:
              type: string
              title: Message
              pattern: "^[a-z][a-z0-9.-]*$"
`

func loadRouted(t *testing.T) *Template {
	t.Helper()
	path := filepath.Join(t.TempDir(), templateManifestName)
	if err := os.WriteFile(path, []byte(routedManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplate(path)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	return tmpl
}

// An array of objects is one field whose element fields keep their YAML
// declaration order, required flags and patterns.
func TestFieldsReadsAListOfObjects(t *testing.T) {
	f := loadRouted(t).Fields()[0]

	if f.Type != "array" || f.MinItems == nil || *f.MinItems != 1 || f.MaxItems != nil {
		t.Fatalf("routes = %+v", f)
	}
	if len(f.Items) != 2 || f.Items[0].Name != "when" || f.Items[1].Name != "message" {
		t.Fatalf("items = %+v, want when then message", f.Items)
	}
	if f.Items[0].Required || !f.Items[1].Required || f.Items[1].Pattern == "" {
		t.Errorf("items = %+v", f.Items)
	}
}

// --set carries a list as JSON; it validates against the schema like any
// other value.
func TestResolveTakesAListFromSet(t *testing.T) {
	tmpl := loadRouted(t)
	values, err := Resolve(tmpl, nil, nil, map[string]any{
		"routes": `[{"message": "order-placed"}, {"message": "order-cancelled", "when": "event.data.reason != 'x'"}]`,
	}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []any{
		map[string]any{"message": "order-placed"},
		map[string]any{"message": "order-cancelled", "when": "event.data.reason != 'x'"},
	}
	if !reflect.DeepEqual(values["routes"], want) {
		t.Errorf("routes = %#v", values["routes"])
	}

	if _, err := Resolve(tmpl, nil, nil, map[string]any{"routes": `[]`}, nil); err == nil {
		t.Error("an empty list passed minItems")
	}
}

// --subscribe seeds a list-shaped message parameter with one route, and
// accepts a pre-set list only when it routes the message.
func TestSeedMessageParametersSeedsOneRoute(t *testing.T) {
	tmpl := loadRouted(t)

	sets := map[string]any{}
	if err := seedMessageParameters(tmpl, []string{"routes"}, sets, "--subscribe", ErrSubscribeMessageConflict, "order-placed"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sets["routes"], []any{map[string]any{"message": "order-placed"}}) {
		t.Errorf("routes = %#v", sets["routes"])
	}

	preset := map[string]any{"routes": `[{"message": "order-cancelled"}]`}
	err := seedMessageParameters(tmpl, []string{"routes"}, preset, "--subscribe", ErrSubscribeMessageConflict, "order-placed")
	if err == nil || !strings.Contains(err.Error(), "does not route it") {
		t.Errorf("err = %v, want a conflict", err)
	}
}

// The prompter asks one element at a time, re-asks a required element field,
// omits an empty optional one, and stops when the user adds no more.
func TestStdinPrompterAsksForAListOneElementAtATime(t *testing.T) {
	f := loadRouted(t).Fields()[0]
	input := strings.Join([]string{
		"",             // route 1 filter: none
		"",             // route 1 message: required, asked again
		"order-placed", // route 1 message
		"y",            // add another
		"event.data.x", // route 2 filter
		"order-cancelled",
		"n", // done
	}, "\n") + "\n"

	v, _, out, err := promptRun(t, input, f)
	if err != nil {
		t.Fatalf("Prompt: %v\n%s", err, out)
	}
	want := []any{
		map[string]any{"message": "order-placed"},
		map[string]any{"message": "order-cancelled", "when": "event.data.x"},
	}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("value = %#v\n%s", v, out)
	}
	if !strings.Contains(out, "a value is required") {
		t.Errorf("no re-ask for the required message:\n%s", out)
	}
}

// A list-shaped message parameter's message field takes the workspace's
// message candidates, filed under the element key.
func TestSuggestOffersMessagesForRoutedMessages(t *testing.T) {
	f := loadRouted(t).Fields()[0]
	facts := &WorkspaceFacts{}
	facts.SetMessageParameters([]string{"routes"})
	facts.AddMessageCandidates([]string{"order-placed"})

	got := Suggest([]FieldSpec{f}, facts, map[string]any{})
	if !reflect.DeepEqual(got[ItemSuggestionKey("routes", "message")], []string{"order-placed"}) {
		t.Errorf("suggestions = %v", got)
	}
	AttachSuggestions(&f, got)
	if !reflect.DeepEqual(f.Items[1].Suggestions, []string{"order-placed"}) {
		t.Errorf("attached = %+v", f.Items)
	}
}

func TestReadRoutes(t *testing.T) {
	routed := ScaffoldEntry{Path: "order-loader", Scaffold: Scaffold{Values: map[string]any{
		KeyRoutes: []any{
			map[string]any{KeyMessage: "order-placed"},
			map[string]any{KeyMessage: "order-cancelled", KeyWhen: "event.data.reason != 'x'"},
		},
	}}}
	got, err := ReadRoutes(routed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []Route{{Message: "order-placed"}, {Message: "order-cancelled", When: "event.data.reason != 'x'"}}) {
		t.Errorf("routes = %+v", got)
	}

	scalar := ScaffoldEntry{Path: "l", Scaffold: Scaffold{Values: map[string]any{KeySubscribes: "order-placed"}}}
	if got, err := ReadRoutes(scalar); err != nil || !reflect.DeepEqual(got, []Route{{Message: "order-placed"}}) {
		t.Errorf("scalar routes = %+v, %v", got, err)
	}

	twice := ScaffoldEntry{Path: "l", Scaffold: Scaffold{Values: map[string]any{KeyRoutes: []any{
		map[string]any{KeyMessage: "order-placed"}, map[string]any{KeyMessage: "order-placed"},
	}}}}
	if _, err := ReadRoutes(twice); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("err = %v, want a duplicate-route error", err)
	}
}
