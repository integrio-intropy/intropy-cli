package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routesSkeleton is one directory per route, named after the route's message
// with the same derivation system assembly uses.
func routesSkeleton(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "Program.cs.tmpl"), "routes: {{ len .routes }}\n")
	writeFile(t, filepath.Join(src, "Routes", "{{ pascal .route.message }}", "Deserializer.cs.tmpl"),
		"// route {{ .routeIndex }}: {{ .route.message }} as {{ pascal .route.message }}\n")
	return src
}

var routesRules = []FileRule{{Path: "Routes/**", Each: "routes", As: "route"}}

func routesValues(messages ...string) map[string]any {
	routes := make([]any, len(messages))
	for i, m := range messages {
		routes[i] = map[string]any{"message": m}
	}
	return map[string]any{"routes": routes}
}

// An expanded path renders once per element, the element and its index bound
// for the path and the body alike; paths outside the rule render once.
func TestRenderFilteredExpandsEachElement(t *testing.T) {
	src := routesSkeleton(t)
	dst := t.TempDir()

	values := routesValues("fluxia.orders.order-placed", "fluxia.orders.order-cancelled")
	if err := RenderFiltered(src, dst, values, routesRules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}

	if got := readFile(t, filepath.Join(dst, "Program.cs")); got != "routes: 2\n" {
		t.Errorf("Program.cs = %q", got)
	}
	placed := readFile(t, filepath.Join(dst, "Routes", "FluxiaOrdersOrderPlaced", "Deserializer.cs"))
	if placed != "// route 0: fluxia.orders.order-placed as FluxiaOrdersOrderPlaced\n" {
		t.Errorf("placed = %q", placed)
	}
	cancelled := readFile(t, filepath.Join(dst, "Routes", "FluxiaOrdersOrderCancelled", "Deserializer.cs"))
	if !strings.HasPrefix(cancelled, "// route 1: ") {
		t.Errorf("cancelled = %q", cancelled)
	}
	mustNotExist(t, filepath.Join(dst, "Routes", "{{ pascal .route.message }}"))
}

// An empty or absent list renders nothing under the rule — not even the
// directory.
func TestRenderFilteredExpandsAnEmptyListToNothing(t *testing.T) {
	for name, values := range map[string]map[string]any{
		"empty":  routesValues(),
		"absent": {"routes": nil},
	} {
		t.Run(name, func(t *testing.T) {
			src := routesSkeleton(t)
			dst := t.TempDir()
			if name == "absent" {
				delete(values, "routes")
				writeFile(t, filepath.Join(src, "Program.cs.tmpl"), "static\n")
			}
			if err := RenderFiltered(src, dst, values, routesRules); err != nil {
				t.Fatalf("RenderFiltered: %v", err)
			}
			mustNotExist(t, filepath.Join(dst, "Routes"))
		})
	}
}

// Two elements landing on one file would silently drop one of them.
func TestRenderFilteredRefusesElementsRenderingToOnePath(t *testing.T) {
	src := routesSkeleton(t)
	dst := t.TempDir()

	err := RenderFiltered(src, dst, routesValues("order.placed", "order-placed"), routesRules)
	if err == nil || !strings.Contains(err.Error(), "more than one list element") {
		t.Fatalf("err = %v, want a duplicate-path error", err)
	}
}

func TestRenderFilteredRefusesANonListEach(t *testing.T) {
	src := routesSkeleton(t)
	err := RenderFiltered(src, t.TempDir(), map[string]any{"routes": "order-placed"}, routesRules)
	if err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Fatalf("err = %v, want a not-a-list error", err)
	}
}

// When gates the whole expansion.
func TestRenderFilteredEachRespectsWhen(t *testing.T) {
	src := routesSkeleton(t)
	dst := t.TempDir()
	rules := []FileRule{{Path: "Routes/**", Each: "routes", As: "route", When: "{{ .withRoutes }}"}}
	values := routesValues("order-placed")
	values["withRoutes"] = false

	if err := RenderFiltered(src, dst, values, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustNotExist(t, filepath.Join(dst, "Routes"))
}

// An update matches each element against the baseline by where it lands, so
// adding a route creates its files and leaves the unchanged route's alone,
// even when the new route is inserted before it.
func TestRenderUpdateExpandsEachAgainstTheBaseline(t *testing.T) {
	src := routesSkeleton(t)
	dst := t.TempDir()
	baseline := routesValues("order-placed")
	if err := RenderFiltered(src, dst, baseline, routesRules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}

	outcomes, err := RenderUpdate(src, dst, routesValues("order-cancelled", "order-placed"), routesRules,
		RenderUpdateOptions{Baseline: baseline})
	if err != nil {
		t.Fatalf("RenderUpdate: %v", err)
	}

	byPath := map[string]FileOutcomeKind{}
	for _, o := range outcomes {
		byPath[o.Path] = o.Outcome
	}
	if byPath["Routes/OrderCancelled/Deserializer.cs"] != OutcomeCreated {
		t.Errorf("new route = %q, want created (outcomes %v)", byPath["Routes/OrderCancelled/Deserializer.cs"], byPath)
	}
	// The placed route's body names its index, which moved from 0 to 1: the
	// file matches the baseline render, so it updates instead of conflicting.
	if byPath["Routes/OrderPlaced/Deserializer.cs"] != OutcomeUpdated {
		t.Errorf("moved route = %q, want updated", byPath["Routes/OrderPlaced/Deserializer.cs"])
	}
	if _, err := os.Stat(filepath.Join(dst, "Routes", "OrderCancelled", "Deserializer.cs")); err != nil {
		t.Errorf("new route not written: %v", err)
	}
}

func TestValidateRejectsAnEachThatIsNotAParameterName(t *testing.T) {
	tmpl := Template{APIVersion: APIVersionV1, Kind: KindTemplate,
		Metadata: Metadata{Name: "x"},
		Spec: Spec{Parameters: map[string]any{"type": "object"},
			Files: []FileRule{{Path: "Routes/**", Each: "{{ .routes }}"}}}}
	if err := tmpl.validate(); err == nil || !strings.Contains(err.Error(), "each must name a parameter") {
		t.Fatalf("validate = %v", err)
	}
}
