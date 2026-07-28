package template

import (
	"os"
	"path/filepath"
	"testing"
)

// A template with no spec.files must render exactly as it did before the filter
// existed. Every pre-existing template relies on this.
func TestRenderFilteredNoRulesRendersEverything(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "a.txt"), "a\n")
	writeFile(t, filepath.Join(src, "dapr", "pubsub.yaml"), "pubsub\n")

	if err := RenderFiltered(src, dst, map[string]any{}, nil); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustExist(t, filepath.Join(dst, "a.txt"))
	mustExist(t, filepath.Join(dst, "dapr", "pubsub.yaml"))
}

func TestRenderFilteredIncludesAndExcludesByWhen(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "pubsub-servicebus.yaml.tmpl"), "type: pubsub.azure.servicebus.topics\n")
	writeFile(t, filepath.Join(src, "pubsub-rabbitmq.yaml.tmpl"), "type: pubsub.rabbitmq\n")

	rules := []FileRule{
		{Path: "pubsub-servicebus.yaml.tmpl", When: `{{ eq .pubsub "servicebus" }}`},
		{Path: "pubsub-rabbitmq.yaml.tmpl", When: `{{ eq .pubsub "rabbitmq" }}`},
	}
	if err := RenderFiltered(src, dst, map[string]any{"pubsub": "rabbitmq"}, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustExist(t, filepath.Join(dst, "pubsub-rabbitmq.yaml"))
	mustNotExist(t, filepath.Join(dst, "pubsub-servicebus.yaml"))
}

// A pruned directory must never have its contents parsed. The malformed body
// below is the proof: an implementation that filters after walking (or after
// rendering) fails this test, while one that returns fs.SkipDir passes.
func TestRenderFilteredPrunesDirectoryBeforeParsing(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "keep.txt"), "keep\n")
	writeFile(t, filepath.Join(src, "secrets", "secret.yaml.tmpl"), "{{ this is not a valid template\n")
	writeFile(t, filepath.Join(src, "secrets", "nested", "deeper.yaml.tmpl"), "{{ also broken\n")

	rules := []FileRule{{Path: "secrets/**", When: "{{ .externalSecrets }}"}}
	if err := RenderFiltered(src, dst, map[string]any{"externalSecrets": false}, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustExist(t, filepath.Join(dst, "keep.txt"))
	mustNotExist(t, filepath.Join(dst, "secrets"))
}

func TestRenderFilteredIncludesSubtreeWhenTrue(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "secrets", "secret.yaml.tmpl"), "name: {{ .name }}\n")
	writeFile(t, filepath.Join(src, "secrets", "nested", "more.yaml"), "more\n")

	rules := []FileRule{{Path: "secrets/**", When: "{{ .externalSecrets }}"}}
	values := map[string]any{"externalSecrets": true, "name": "acme"}
	if err := RenderFiltered(src, dst, values, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	if got := readFile(t, filepath.Join(dst, "secrets", "secret.yaml")); got != "name: acme\n" {
		t.Errorf("secret.yaml = %q", got)
	}
	mustExist(t, filepath.Join(dst, "secrets", "nested", "more.yaml"))
}

// The first rule whose Path matches decides, so a specific rule can override a
// broad one placed after it.
func TestRenderFilteredFirstMatchWins(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "dapr", "keep.yaml"), "keep\n")
	writeFile(t, filepath.Join(src, "dapr", "drop.yaml"), "drop\n")

	rules := []FileRule{
		{Path: "dapr/keep.yaml", When: "{{ true }}"},
		{Path: "dapr/*.yaml", When: "{{ false }}"},
	}
	if err := RenderFiltered(src, dst, map[string]any{}, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustExist(t, filepath.Join(dst, "dapr", "keep.yaml"))
	mustNotExist(t, filepath.Join(dst, "dapr", "drop.yaml"))
}

// A path no rule matches is included, so authors only declare the conditional
// parts of a skeleton.
func TestRenderFilteredUnmatchedPathIsIncluded(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "kustomization.yaml"), "resources: []\n")
	writeFile(t, filepath.Join(src, "optional.yaml"), "optional\n")

	rules := []FileRule{{Path: "optional.yaml", When: "{{ false }}"}}
	if err := RenderFiltered(src, dst, map[string]any{}, rules); err != nil {
		t.Fatalf("RenderFiltered: %v", err)
	}
	mustExist(t, filepath.Join(dst, "kustomization.yaml"))
	mustNotExist(t, filepath.Join(dst, "optional.yaml"))
}

// missingkey=error means a typo'd value name is a loud failure, not a silent
// skip — the whole reason a when is a template rather than a bespoke DSL.
func TestRenderFilteredUnknownValueErrors(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "a\n")

	rules := []FileRule{{Path: "a.txt", When: "{{ .doesNotExist }}"}}
	if err := RenderFiltered(src, dst, map[string]any{"other": 1}, rules); err == nil {
		t.Fatal("expected an error for an undefined value in when")
	}
}

func TestTruthy(t *testing.T) {
	for _, tc := range []struct {
		rendered string
		want     bool
	}{
		{"", false},
		{"  ", false},
		{"false", false},
		{"0", false},
		{"<no value>", false},
		{"true", true},
		{"1", true},
		{"yes", true},
		{"servicebus", true},
	} {
		if got := truthy(tc.rendered); got != tc.want {
			t.Errorf("truthy(%q) = %v, want %v", tc.rendered, got, tc.want)
		}
	}
}

func TestMatchSkeletonPath(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"a.txt", "a.txt", true},
		{"a.txt", "b.txt", false},
		{"dapr/*.yaml", "dapr/pubsub.yaml", true},
		// * must not cross a separator, or a narrow rule would silently prune a subtree.
		{"dapr/*.yaml", "dapr/nested/pubsub.yaml", false},
		// /** matches the directory itself, which is what lets the walk prune it.
		{"secrets/**", "secrets", true},
		{"secrets/**", "secrets/secret.yaml", true},
		{"secrets/**", "secrets/nested/more.yaml", true},
		{"secrets/**", "secretstore.yaml", false},
		// The source path is matched, so the .tmpl suffix is part of it.
		{"pubsub.yaml.tmpl", "pubsub.yaml.tmpl", true},
		{"pubsub.yaml", "pubsub.yaml.tmpl", false},
	} {
		if got := matchSkeletonPath(tc.pattern, tc.rel); got != tc.want {
			t.Errorf("matchSkeletonPath(%q, %q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
		}
	}
}

// A when depends only on the resolved values, so each rule is evaluated once no
// matter how many paths it is tested against.
func TestSkeletonFilterCachesRuleEvaluation(t *testing.T) {
	f := newSkeletonFilter([]FileRule{{Path: "dapr/*.yaml", When: "{{ true }}"}}, map[string]any{})
	for _, rel := range []string{"dapr/a.yaml", "dapr/b.yaml", "dapr/c.yaml"} {
		if _, err := f.include(rel); err != nil {
			t.Fatalf("include(%q): %v", rel, err)
		}
	}
	if len(f.cache) != 1 {
		t.Errorf("cache size = %d, want 1", len(f.cache))
	}
}

func TestLoadTemplatePreservesFiles(t *testing.T) {
	tmpl := loadTemplateBody(t, `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: deploy-host
spec:
  parameters:
    type: object
    properties:
      pubsub: { type: string }
  files:
    - path: base/dapr/pubsub-servicebus.yaml.tmpl
      when: '{{ eq .pubsub "servicebus" }}'
    - path: base/secrets/**
      when: '{{ .externalSecrets }}'
`)
	if len(tmpl.Spec.Files) != 2 {
		t.Fatalf("Spec.Files = %d, want 2", len(tmpl.Spec.Files))
	}
	if tmpl.Spec.Files[0].Path != "base/dapr/pubsub-servicebus.yaml.tmpl" {
		t.Errorf("Files[0].Path = %q", tmpl.Spec.Files[0].Path)
	}
	if tmpl.Spec.Files[1].When != "{{ .externalSecrets }}" {
		t.Errorf("Files[1].When = %q", tmpl.Spec.Files[1].When)
	}
}

func TestLoadTemplateRejectsBadFileRules(t *testing.T) {
	for name, files := range map[string]string{
		"missing path": `
    - when: '{{ true }}'`,
		"missing when": `
    - path: base/dapr/pubsub.yaml.tmpl`,
		"malformed when": `
    - path: base/dapr/pubsub.yaml.tmpl
      when: '{{ eq .pubsub '`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `
apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: x
spec:
  parameters:
    type: object
    properties: {}
  files:` + files + "\n"
			dir := t.TempDir()
			path := filepath.Join(dir, templateManifestName)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadTemplate(path); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func loadTemplateBody(t *testing.T, body string) *Template {
	t.Helper()
	path := filepath.Join(t.TempDir(), templateManifestName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadTemplate(path)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	return tmpl
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s not to exist", path)
	}
}
