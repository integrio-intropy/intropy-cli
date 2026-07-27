package gitops

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

const validComponentYAML = `schemaVersion: 1
name: order-extractor
sourcePaths:
  - integrations/domains/orders/order-flow/order-extractor/
images:
  - name: harbor.intropy.io/integrations/order-extractor
environments: [dev, staging, prod]
`

func writeDeployYAML(t *testing.T, root, content string) {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(root, DeployFileName), content)
}

func TestLoadDeployConfig(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, gitopstest.DeployYAML)

	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "harbor.intropy.io" {
		t.Errorf("Registry = %q", cfg.Registry)
	}
	if cfg.Argocd.AppNamespace != "customer-fluxia" {
		t.Errorf("Argocd.AppNamespace = %q", cfg.Argocd.AppNamespace)
	}
	if got := cfg.EnvironmentNames(); strings.Join(got, ",") != "dev,prod,staging" {
		t.Errorf("EnvironmentNames() = %v, want them sorted", got)
	}

	prod, err := cfg.Environment("prod")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Sync != SyncManual {
		t.Errorf("prod.Sync = %q, want %q", prod.Sync, SyncManual)
	}
	if !prod.RequireSourceHealthy {
		t.Error("prod.RequireSourceHealthy should be true")
	}
	if strings.Join(prod.PromotesFrom, ",") != "staging" {
		t.Errorf("prod.PromotesFrom = %v", prod.PromotesFrom)
	}
}

// A repository with no deploy.yaml is not one this CLI may write to, and the
// message has to say so rather than complain about a missing file.
func TestLoadDeployConfigMissingFile(t *testing.T) {
	root := t.TempDir()
	_, err := LoadDeployConfig(root)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not a GitOps repository") {
		t.Errorf("error %q should explain the repository is not usable", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("the not-a-repository message should replace the raw fs error")
	}
}

func TestUnknownEnvironmentListsWhatExists(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, gitopstest.DeployYAML)
	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.Environment("qa")
	if err == nil {
		t.Fatal("expected an error for an unknown environment")
	}
	for _, want := range []string{"dev", "staging", "prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should list %q", err, want)
		}
	}
}

func TestLoadDeployConfigRejections(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing schemaVersion",
			yaml:    "registry: r\nenvironments:\n  dev:\n    sync: auto\n",
			wantErr: "schemaVersion is required",
		},
		{
			name:    "future schemaVersion",
			yaml:    "schemaVersion: 99\nregistry: r\nenvironments:\n  dev:\n    sync: auto\n",
			wantErr: "upgrade intropy",
		},
		{
			name:    "missing registry",
			yaml:    "schemaVersion: 1\nenvironments:\n  dev:\n    sync: auto\n",
			wantErr: "registry is required",
		},
		{
			name:    "no environments",
			yaml:    "schemaVersion: 1\nregistry: r\n",
			wantErr: "environments is required",
		},
		{
			name:    "missing sync policy",
			yaml:    "schemaVersion: 1\nregistry: r\nenvironments:\n  dev: {}\n",
			wantErr: "no sync policy",
		},
		{
			name:    "unrecognised sync policy",
			yaml:    "schemaVersion: 1\nregistry: r\nenvironments:\n  dev:\n    sync: sometimes\n",
			wantErr: `sync "sometimes"`,
		},
		{
			// A promotesFrom pointing at a typo'd environment would fail only
			// when someone eventually ran promote, long after the mistake.
			name:    "promotesFrom names an undefined environment",
			yaml:    "schemaVersion: 1\nregistry: r\nenvironments:\n  prod:\n    sync: manual\n    promotesFrom: [stagng]\n",
			wantErr: "which is not defined",
		},
		{
			name:    "unknown key",
			yaml:    "schemaVersion: 1\nregistry: r\nregistyr: typo\nenvironments:\n  dev:\n    sync: auto\n",
			wantErr: "registyr",
		},
		{
			name:    "empty file",
			yaml:    "",
			wantErr: "file is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDeployYAML(t, root, tc.yaml)
			_, err := LoadDeployConfig(root)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should mention %q", err, tc.wantErr)
			}
		})
	}
}

// The scratch flag has no defined behaviour yet, but a repository that already
// sets it must parse rather than be rejected as malformed.
func TestScratchFlagIsCarried(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, "schemaVersion: 1\nregistry: r\nenvironments:\n  pr-123:\n    sync: auto\n    scratch: true\n")
	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cfg.Environment("pr-123")
	if err != nil {
		t.Fatal(err)
	}
	if !env.Scratch {
		t.Error("scratch should be carried through")
	}
}

func TestLoadComponentConfig(t *testing.T) {
	dir := t.TempDir()
	gittest.WriteFile(t, filepath.Join(dir, ComponentFileName), validComponentYAML)

	cfg, err := LoadComponentConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "order-extractor" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if len(cfg.Images) != 1 || cfg.Images[0].Name != "harbor.intropy.io/integrations/order-extractor" {
		t.Errorf("Images = %+v", cfg.Images)
	}
	if !cfg.SupportsEnvironment("dev") || cfg.SupportsEnvironment("qa") {
		t.Errorf("SupportsEnvironment wrong for %v", cfg.Environments)
	}
}

// A colon in a registry host is a port, not a tag. Rejecting it would rule out
// every registry not on the default port, local ones included.
func TestImageNameAcceptsRegistryPort(t *testing.T) {
	cases := []struct {
		image string
		want  bool // carries a tag or digest
	}{
		{"harbor.intropy.io/integrations/order-extractor", false},
		{"localhost:5555/integrations/order-extractor", false},
		{"harbor.example.com:8443/integrations/order-extractor", false},
		{"order-extractor", false},
		{"harbor.intropy.io/integrations/order-extractor:latest", true},
		{"localhost:5555/integrations/order-extractor:v1.2.3", true},
		{"harbor.intropy.io/integrations/order-extractor@sha256:abc", true},
		{"localhost:5555/order-extractor@sha256:abc", true},
	}
	for _, tc := range cases {
		if got := hasTagOrDigest(tc.image); got != tc.want {
			t.Errorf("hasTagOrDigest(%q) = %v, want %v", tc.image, got, tc.want)
		}
	}

	// And the full validation path accepts a ported registry.
	dir := t.TempDir()
	gittest.WriteFile(t, filepath.Join(dir, ComponentFileName),
		"schemaVersion: 1\nname: c\nimages: [{name: 'localhost:5555/integrations/c'}]\nenvironments: [dev]\n")
	if _, err := LoadComponentConfig(dir); err != nil {
		t.Errorf("a registry with an explicit port should be accepted: %v", err)
	}
}

func TestLoadComponentConfigRejections(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing name",
			yaml:    "schemaVersion: 1\nimages: [{name: r/i}]\nenvironments: [dev]\n",
			wantErr: "name is required",
		},
		{
			// Without an images entry there is nothing to pin, and the deploy
			// would "succeed" having changed nothing.
			name:    "no images",
			yaml:    "schemaVersion: 1\nname: c\nenvironments: [dev]\n",
			wantErr: "nothing to pin",
		},
		{
			// A tag here would be pinned into the overlay as part of the image
			// name and quietly defeat digest pinning.
			name:    "image carries a tag",
			yaml:    "schemaVersion: 1\nname: c\nimages: [{name: 'r/i:latest'}]\nenvironments: [dev]\n",
			wantErr: "without a tag or digest",
		},
		{
			name:    "image carries a digest",
			yaml:    "schemaVersion: 1\nname: c\nimages: [{name: 'r/i@sha256:abc'}]\nenvironments: [dev]\n",
			wantErr: "without a tag or digest",
		},
		{
			name:    "no environments",
			yaml:    "schemaVersion: 1\nname: c\nimages: [{name: r/i}]\n",
			wantErr: "environments is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gittest.WriteFile(t, filepath.Join(dir, ComponentFileName), tc.yaml)
			_, err := LoadComponentConfig(dir)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should mention %q", err, tc.wantErr)
			}
		})
	}
}
