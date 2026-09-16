//go:build e2e

package template

// Gate proofs against the real template library (WS-1.3): the shipped
// message flags must pass the message-parameters gate, seed correctly, and
// write scalar-shaped records against the actual extractor/loader manifests,
// not the fixtures that pin the contract. Fixtures stay for the same reason
// — they are the contract's own test.
//
// Run with:
//
//	INTROPY_TEMPLATES_DIR=</path/to/intropy-templates> go test -tags e2e ./internal/template/ -run TestReal -v
//
// The fetch resolves through the same seams Create uses in production: the
// local checkout is cloned as the library at its current branch, so the run
// exercises the branch's committed HEAD. Uncommitted edits in the checkout
// are outside what these tests see.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realTemplatesSource points the library-fetch seams at a local
// intropy-templates checkout (INTROPY_TEMPLATES_DIR). The checkout's current
// branch cannot be a pin itself — the fetch clones into a temp directory
// named after the pin, and a slash breaks that name — so the branch is first
// cloned into a throwaway mirror and tagged with a plain name. Set the second
// return value on every CreateOptions.Version: the option overrides
// SourceOptions.Version, and an empty pin would send latest-release
// resolution to GitHub. ResolveTag passes the pin through verbatim and the
// clone comes from the local path, so nothing here touches the network.
func realTemplatesSource(t *testing.T) (SourceOptions, string) {
	const pin = "message-first-e2e"
	t.Helper()
	dir := os.Getenv("INTROPY_TEMPLATES_DIR")
	if dir == "" {
		t.Skip("INTROPY_TEMPLATES_DIR not set")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", abs, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("read the checkout's branch: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		t.Fatalf("INTROPY_TEMPLATES_DIR %s is on a detached HEAD; point it at a branch", abs)
	}
	mirror := filepath.Join(t.TempDir(), "templates-mirror")
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", branch, abs, mirror).CombinedOutput(); err != nil {
		t.Fatalf("mirror the checkout at %s: %v\n%s", branch, err, out)
	}
	if out, err := exec.Command("git", "-C", mirror, "tag", pin, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("pin the mirrored checkout: %v\n%s", err, out)
	}
	return SourceOptions{
		RepoURL: "file://" + mirror,
		// Fresh cache per test: the checkout may move between runs, and a
		// shared cache would pin the first-seen HEAD forever.
		CacheRoot: t.TempDir(),
	}, pin
}

// Happy path, producer side: --publishes against the real extractor manifest
// passes the gate, seeds the message parameter, and lands a record whose
// scalar wiring matches the rendered constants.
func TestRealManifestPublishesExtractor(t *testing.T) {
	src, version := realTemplatesSource(t)
	outDir := filepath.Join(t.TempDir(), "order-sweep")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "extractor",
		OutputDir:        outDir,
		Version:          version,
		NoInput:          true,
		Stderr:           &stderr,
		Source:           src,
		SetValues:        map[string]any{"name": "OrderSweep", "organization": "Maxbo"},
		PublishesMessage: "orders",
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Values[KeyPublishes]; got != "orders" {
		t.Errorf("values.publishes = %v, want the --publishes message", got)
	}
	if _, has := record.Values["subscribe"]; has {
		t.Errorf("scaffold record grew a legacy subscribe block: %v", record.Values)
	}
	if record.BlockKind != BlockKindExtractor || record.DataFlow != "in" {
		t.Errorf("record kind/data-flow = %q/%q, want extractor/in", record.BlockKind, record.DataFlow)
	}

	constants, err := os.ReadFile(filepath.Join(outDir, "src", "Constants.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`public const string EventType = "orders";`,
		`public const string PubSubName = "pubsub";`,
		`public const string TopicName = "orders";`,
	} {
		if !strings.Contains(string(constants), want) {
			t.Errorf("Constants.cs missing %q:\n%s", want, constants)
		}
	}
}

// Happy path, consumer side: --subscribe against the real loader manifest.
func TestRealManifestSubscribeLoader(t *testing.T) {
	src, version := realTemplatesSource(t)
	outDir := filepath.Join(t.TempDir(), "order-file-loader")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "loader",
		OutputDir:        outDir,
		Version:          version,
		NoInput:          true,
		Stderr:           &stderr,
		Source:           src,
		SetValues:        map[string]any{"name": "OrderFileLoader", "organization": "Maxbo"},
		SubscribeMessage: "orders",
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Values[KeySubscribes]; got != "orders" {
		t.Errorf("values.subscribes = %v, want the --subscribe message", got)
	}
	if _, has := record.Values[KeyPublishes]; has {
		t.Errorf("loader record grew a publishes value: %v", record.Values)
	}

	constants, err := os.ReadFile(filepath.Join(outDir, "src", "Constants.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`public const string MessageName = "orders";`,
		`public const string PubSubName = "pubsub";`,
		`public const string SubscriptionRoute = "/events/" + TopicName;`,
	} {
		if !strings.Contains(string(constants), want) {
			t.Errorf("Constants.cs missing %q:\n%s", want, constants)
		}
	}
}

// The direction gate: --subscribe against the real extractor and --publishes
// against the real loader are usage errors, and nothing lands on disk.
// A template flag that silently vanished would leave a record whose wiring
// contradicts the rendered code.
func TestRealManifestDirectionGates(t *testing.T) {
	src, version := realTemplatesSource(t)
	runs := []struct {
		name      string
		tmpl      string
		subscribe string
		publishes string
		hint      string
	}{
		{name: "subscribe against extractor", tmpl: "extractor", subscribe: "orders", hint: "use --publishes"},
		{name: "publishes against loader", tmpl: "loader", publishes: "orders", hint: "use --subscribe"},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "out")
			var stderr bytes.Buffer
			err := Create(context.Background(), CreateOptions{
				Template:         r.tmpl,
				OutputDir:        outDir,
				Version:          version,
				NoInput:          true,
				Stderr:           &stderr,
				Source:           src,
				SetValues:        map[string]any{"name": "OrderSweep", "organization": "Maxbo"},
				SubscribeMessage: r.subscribe,
				PublishesMessage: r.publishes,
			})
			if !strings.Contains(err.Error(), ErrMessageDirection.Error()) {
				t.Fatalf("err = %v, want %v", err, ErrMessageDirection)
			}
			if !strings.Contains(err.Error(), r.hint) {
				t.Errorf("error %q should suggest %q", err, r.hint)
			}
			if _, statErr := os.Stat(filepath.Join(outDir, ScaffoldRelPath)); !os.IsNotExist(statErr) {
				t.Errorf("a gate refusal must not write a scaffold record (stat err = %v)", statErr)
			}
		})
	}
}

// A real manifest without the message-params label (the hello-world
// template, whose manifest name is dapr-minimal-api) refuses the flags with
// ErrNoMessageParameters and the pinned-version guidance, before anything
// renders.
func TestRealManifestNoMessageParameters(t *testing.T) {
	src, version := realTemplatesSource(t)
	outDir := filepath.Join(t.TempDir(), "out")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "hello-world",
		OutputDir:        outDir,
		Version:          version,
		NoInput:          true,
		Stderr:           &stderr,
		Source:           src,
		SetValues:        map[string]any{"name": "HelloWorld"},
		SubscribeMessage: "orders",
	})
	if !strings.Contains(err.Error(), ErrNoMessageParameters.Error()) {
		t.Fatalf("err = %v, want %v", err, ErrNoMessageParameters)
	}
	for _, want := range []string{"dapr-minimal-api", TemplateMessageParamsLabel, "check the pinned template version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Errorf("the gate refusal must not create the output directory (stat err = %v)", statErr)
	}
}

// A seeded message parameter that conflicts with an explicit --set fails
// with the seed-conflict error naming both messages — a disagreement is a
// mistake to fix, never a silent overwrite.
func TestRealManifestSeedConflict(t *testing.T) {
	src, version := realTemplatesSource(t)
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:  "extractor",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   version,
		NoInput:   true,
		Stderr:    &stderr,
		Source:    src,
		SetValues: map[string]any{
			"name":         "OrderSweep",
			"organization": "Maxbo",
			"publishes":    "catalog-updated",
		},
		PublishesMessage: "orders",
	})
	if !strings.Contains(err.Error(), ErrMessageParameterConflict.Error()) {
		t.Fatalf("err = %v, want %v", err, ErrMessageParameterConflict)
	}
	for _, want := range []string{"orders", "catalog-updated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// The message identity must be a lowercase DNS-1123 subdomain: it defaults
// the endpoint channel and the topology validates the name at Build. The
// scaffold is where that fails loud, not `dotnet run -- graph`.
func TestRealManifestMessagePatternRejected(t *testing.T) {
	src, version := realTemplatesSource(t)
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "extractor",
		OutputDir:        filepath.Join(t.TempDir(), "bad-name"),
		Version:          version,
		NoInput:          true,
		Stderr:           &stderr,
		Source:           src,
		SetValues:        map[string]any{"name": "OrderSweep", "organization": "Maxbo"},
		PublishesMessage: "OrderCreated", // PascalCase: not a DNS-1123 subdomain
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()+"\n"+strings.ToLower(stderr.String())), "pattern") {
		t.Fatalf("err = %v, want the manifest pattern rejection\nstderr: %s", err, stderr.String())
	}
}
