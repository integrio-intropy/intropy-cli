package argocd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliConfig = `contexts:
- name: argocd.local.dev:30453
  server: argocd.local.dev:30453
  user: argocd.local.dev:30453
- name: argocd.intropy.io
  server: argocd.intropy.io
  user: argocd.intropy.io
current-context: argocd.intropy.io
prompts-enabled: false
servers:
- grpc-web-root-path: ""
  insecure: true
  server: argocd.local.dev:30453
- grpc-web-root-path: ""
  server: argocd.intropy.io
users:
- auth-token: local-token
  name: argocd.local.dev:30453
- auth-token: intropy-token
  name: argocd.intropy.io
  refresh-token: refresh
`

// writeCLIConfig points ARGOCD_CONFIG at a temporary file, and clears the
// environment overrides so each case starts from a known state.
func writeCLIConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ARGOCD_CONFIG", path)
	t.Setenv(EnvServer, "")
	t.Setenv(EnvAuthToken, "")
	return path
}

func TestLoadCredentialsFollowsCurrentContext(t *testing.T) {
	writeCLIConfig(t, cliConfig)

	creds, err := LoadCredentials("")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Server != "argocd.intropy.io" {
		t.Errorf("Server = %q, want the current context's", creds.Server)
	}
	if creds.Token != "intropy-token" {
		t.Errorf("Token = %q", creds.Token)
	}
	if creds.Insecure {
		t.Error("Insecure should be false for this server")
	}
}

// deploy.yaml's argocd.server wins over the CLI's current context: it travels
// with the repository whose overlays are being deployed, so it is the more
// specific statement of where this deployment belongs.
func TestLoadCredentialsExplicitServerWins(t *testing.T) {
	writeCLIConfig(t, cliConfig)

	creds, err := LoadCredentials("argocd.local.dev:30453")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Server != "argocd.local.dev:30453" {
		t.Errorf("Server = %q", creds.Server)
	}
	if creds.Token != "local-token" {
		t.Errorf("Token = %q, want the token for the requested server", creds.Token)
	}
	// The local dev clusters serve self-signed certificates, and the CLI config
	// is where that fact lives.
	if !creds.Insecure {
		t.Error("Insecure should follow the CLI config for this server")
	}
}

func TestLoadCredentialsTokenFromEnvironmentWins(t *testing.T) {
	writeCLIConfig(t, cliConfig)
	t.Setenv(EnvAuthToken, "ci-token")

	creds, err := LoadCredentials("argocd.intropy.io")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "ci-token" {
		t.Errorf("Token = %q, want the environment to win over the CLI config", creds.Token)
	}
}

// Precedence lives in ResolveServer so it is applied once, matching the CLI's
// flag > environment > file rule elsewhere.
func TestResolveServer(t *testing.T) {
	t.Setenv(EnvServer, "")
	if got := ResolveServer("", "argocd.from-deploy-yaml"); got != "argocd.from-deploy-yaml" {
		t.Errorf("ResolveServer = %q, want deploy.yaml's value", got)
	}

	t.Setenv(EnvServer, "argocd.from-env")
	if got := ResolveServer("", "argocd.from-deploy-yaml"); got != "argocd.from-env" {
		t.Errorf("ResolveServer = %q, want the environment to beat deploy.yaml", got)
	}
	if got := ResolveServer("argocd.from-flag", "argocd.from-deploy-yaml"); got != "argocd.from-flag" {
		t.Errorf("ResolveServer = %q, want the flag to beat everything", got)
	}

	t.Setenv(EnvServer, "")
	if got := ResolveServer("", ""); got != "" {
		t.Errorf("ResolveServer = %q, want empty so the CLI context is followed", got)
	}
}

// With no config file at all the environment has to supply everything — which
// is the CI case, where nobody has run `argocd login`.
func TestLoadCredentialsFromEnvironmentOnly(t *testing.T) {
	writeCLIConfig(t, "")
	t.Setenv(EnvServer, "argocd.ci.example.com")
	t.Setenv(EnvAuthToken, "ci-token")

	creds, err := LoadCredentials(ResolveServer("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if creds.Server != "argocd.ci.example.com" || creds.Token != "ci-token" {
		t.Errorf("creds = %+v", creds)
	}
}

func TestLoadCredentialsMissingTokenSaysHowToGetOne(t *testing.T) {
	writeCLIConfig(t, `current-context: argocd.other.example.com
contexts:
- name: argocd.other.example.com
  server: argocd.other.example.com
servers:
- server: argocd.other.example.com
users: []
`)

	_, err := LoadCredentials("")
	if err == nil {
		t.Fatal("expected an error when no token is available")
	}
	if !strings.Contains(err.Error(), "argocd login") {
		t.Errorf("error %q should tell the user how to obtain a token", err)
	}
}

func TestLoadCredentialsNoConfigAndNoEnvironment(t *testing.T) {
	writeCLIConfig(t, "")

	_, err := LoadCredentials("")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"argocd login", EnvServer, EnvAuthToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// argocd owns this file format and adds keys to it. Rejecting an unknown key
// would break for a reason the user cannot act on, so unlike this CLI's own
// files, unknown fields are tolerated.
func TestLoadCredentialsToleratesUnknownKeys(t *testing.T) {
	writeCLIConfig(t, cliConfig+"some-future-argocd-key: value\n")

	if _, err := LoadCredentials(""); err != nil {
		t.Errorf("unknown keys should be tolerated, got %v", err)
	}
}

func TestLoadCredentialsRejectsMalformedConfig(t *testing.T) {
	writeCLIConfig(t, "contexts: [unclosed\n")

	if _, err := LoadCredentials(""); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestNewClientScheme(t *testing.T) {
	cases := []struct {
		creds Credentials
		want  string
	}{
		{Credentials{Server: "argocd.example.com"}, "https://argocd.example.com"},
		{Credentials{Server: "argocd.example.com", PlainText: true}, "http://argocd.example.com"},
		{Credentials{Server: "argocd.example.com:8443"}, "https://argocd.example.com:8443"},
		// A full URL is tolerated even though argocd stores host[:port].
		{Credentials{Server: "http://argocd.example.com:8080"}, "http://argocd.example.com:8080"},
	}
	for _, tc := range cases {
		c, err := NewClient(Options{Credentials: tc.creds})
		if err != nil {
			t.Fatal(err)
		}
		if got := c.base.String(); got != tc.want {
			t.Errorf("NewClient(%+v) base = %q, want %q", tc.creds, got, tc.want)
		}
	}
}

func TestNewClientRequiresServer(t *testing.T) {
	if _, err := NewClient(Options{}); err == nil {
		t.Fatal("expected an error with no server")
	}
}
