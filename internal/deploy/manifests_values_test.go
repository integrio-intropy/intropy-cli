package deploy

import (
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// The deploy.yaml scaffolding facts reach the templates: the image namespace,
// pull secret and OTEL endpoint as parameter seeds, the namespace through each
// environment's .gitops map. Empty values must stay empty, so seedDeclaredParams
// drops them and the template's own default applies.
func TestManifestValuesCarryDeployScaffoldingFacts(t *testing.T) {
	facts := manifestFacts{
		Registry:        "harbor.intropy.io",
		Platform:        gitops.PlatformConfig{Pubsub: "rabbitmq", OtelEndpoint: "http://tempo.monitoring.svc.cluster.local:4317"},
		ImageNamespace:  "intropy-poc",
		ImagePullSecret: "harbor-intropy-robot-credentials",
		Namespaces:      map[string]string{"test": "test-uppsala-integrations", "prod": ""},
	}

	seeds := manifestSeeds(facts, "invoice-extractor", nil)
	if got := seeds["imageNamespace"]; got != "intropy-poc" {
		t.Errorf("imageNamespace seed = %v", got)
	}
	if got := seeds["imagePullSecret"]; got != "harbor-intropy-robot-credentials" {
		t.Errorf("imagePullSecret seed = %v", got)
	}
	if got := seeds["otelEndpoint"]; got != "http://tempo.monitoring.svc.cluster.local:4317" {
		t.Errorf("otelEndpoint seed = %v", got)
	}

	if got := facts.gitopsMap("invoice-extractor", "test")["namespace"]; got != "test-uppsala-integrations" {
		t.Errorf("test namespace = %v", got)
	}
	if got := facts.gitopsMap("invoice-extractor", "prod")["namespace"]; got != "" {
		t.Errorf("prod namespace = %v, want empty for the template default", got)
	}
}

// Without the fields, nothing changes: the seeds are blank, which
// seedDeclaredParams treats as absent.
func TestManifestValuesWithoutDeployScaffoldingFacts(t *testing.T) {
	seeds := manifestSeeds(manifestFacts{Registry: "harbor.intropy.io"}, "c", nil)
	for _, key := range []string{"imageNamespace", "imagePullSecret", "otelEndpoint"} {
		if !isBlank(seeds[key]) {
			t.Errorf("%s seed = %v, want blank", key, seeds[key])
		}
	}
	if got := (manifestFacts{}).gitopsMap("c", "local")["namespace"]; got != "" {
		t.Errorf("local namespace = %v, want empty", got)
	}
}
