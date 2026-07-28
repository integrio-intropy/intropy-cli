package gitops

import (
	"strings"
	"testing"
)

// platform is optional: every deploy.yaml written before it existed must still
// load, with the zero value standing for "unspecified".
func TestDeployConfigWithoutPlatform(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, `schemaVersion: 1
registry: harbor.intropy.io
argocd:
  server: argocd.example.com
  appNamespace: customer-acme
environments:
  dev: {sync: auto}
`)
	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform != (PlatformConfig{}) {
		t.Errorf("Platform = %+v, want the zero value", cfg.Platform)
	}
}

func TestDeployConfigWithPlatform(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, `schemaVersion: 1
registry: harbor.intropy.io
platform:
  provider: azure
  pubsub: servicebus
  secretStore: azure.keyvault
argocd:
  server: argocd.example.com
  appNamespace: customer-acme
environments:
  dev: {sync: auto}
  prod: {sync: manual, promotesFrom: [dev]}
`)
	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	want := PlatformConfig{Provider: "azure", Pubsub: "servicebus", SecretStore: "azure.keyvault"}
	if cfg.Platform != want {
		t.Errorf("Platform = %+v, want %+v", cfg.Platform, want)
	}
}

// The values are template-specific and deliberately unvalidated, so an unknown
// provider must load rather than fail. A misspelled *key*, however, is still a
// loud error — that is what KnownFields buys.
func TestPlatformValuesAreNotValidatedButKeysAre(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, `schemaVersion: 1
registry: harbor.intropy.io
platform:
  provider: some-future-cloud
  pubsub: kafka
environments:
  dev: {sync: auto}
`)
	cfg, err := LoadDeployConfig(root)
	if err != nil {
		t.Fatalf("an unrecognised provider should load: %v", err)
	}
	if cfg.Platform.Provider != "some-future-cloud" {
		t.Errorf("Provider = %q", cfg.Platform.Provider)
	}

	root = t.TempDir()
	writeDeployYAML(t, root, `schemaVersion: 1
registry: harbor.intropy.io
platform:
  provider: azure
  pubsubb: servicebus
environments:
  dev: {sync: auto}
`)
	if _, err := LoadDeployConfig(root); err == nil {
		t.Fatal("expected an error for a misspelled key under platform")
	}
}

// An environment may not carry its own platform: Dapr components live in base/
// and are identical in every environment, so an override would have nothing to
// act on and would invite dev and prod to diverge structurally.
func TestEnvironmentPlatformOverrideIsRejected(t *testing.T) {
	root := t.TempDir()
	writeDeployYAML(t, root, `schemaVersion: 1
registry: harbor.intropy.io
platform:
  provider: beebyte
  pubsub: rabbitmq
environments:
  dev:
    sync: auto
    platform:
      pubsub: in-memory
`)
	_, err := LoadDeployConfig(root)
	if err == nil {
		t.Fatal("expected an error for a per-environment platform block")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Errorf("error should name the offending key: %v", err)
	}
}
