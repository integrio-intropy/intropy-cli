// Package argocd talks to the ArgoCD API over HTTP and waits for an
// application to converge on a specific git revision.
//
// Credentials are borrowed from the argocd CLI's own configuration rather than
// asked for again: if `argocd login` has been run, this works with no extra
// setup. ARGOCD_SERVER and ARGOCD_AUTH_TOKEN override it — those are argocd's
// own variable names, kept deliberately so an existing CI setup needs no
// changes.
package argocd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Environment variables borrowed from the argocd CLI.
const (
	EnvServer    = "ARGOCD_SERVER"
	EnvAuthToken = "ARGOCD_AUTH_TOKEN"
)

// CLIConfig is the subset of ~/.config/argocd/config this package reads.
type CLIConfig struct {
	CurrentContext string       `yaml:"current-context"`
	Contexts       []CLIContext `yaml:"contexts"`
	Servers        []CLIServer  `yaml:"servers"`
	Users          []CLIUser    `yaml:"users"`
}

// CLIContext binds a context name to a server and user.
type CLIContext struct {
	Name   string `yaml:"name"`
	Server string `yaml:"server"`
	User   string `yaml:"user"`
}

// CLIServer records how to reach one server.
type CLIServer struct {
	Server string `yaml:"server"`

	// Insecure skips TLS verification. Set for the local dev clusters, which
	// serve self-signed certificates.
	Insecure bool `yaml:"insecure"`

	// PlainText means the server speaks HTTP rather than HTTPS.
	PlainText bool `yaml:"plain-text"`
}

// CLIUser holds one server's credentials.
type CLIUser struct {
	Name      string `yaml:"name"`
	AuthToken string `yaml:"auth-token"`
}

// Credentials are what a Client needs to reach a server.
type Credentials struct {
	Server    string
	Token     string
	Insecure  bool
	PlainText bool
}

// CLIConfigPath returns the argocd CLI's configuration file, honouring
// ARGOCD_CONFIG and XDG_CONFIG_HOME the same way argocd itself does.
func CLIConfigPath() (string, error) {
	if p := os.Getenv("ARGOCD_CONFIG"); p != "" {
		return p, nil
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "argocd", "config"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "argocd", "config"), nil
}

// LoadCredentials resolves how to authenticate against a server.
//
// server is taken as already resolved — the caller owns the flag > environment >
// file precedence, so it lives in one place rather than being reapplied here.
// Empty means "follow the CLI's current context". The token comes from the
// environment or the CLI configuration; there is nowhere else to get one.
func LoadCredentials(server string) (Credentials, error) {
	creds := Credentials{
		Server: server,
		Token:  os.Getenv(EnvAuthToken),
	}

	cfg, path, err := loadCLIConfig()
	if err != nil {
		return Credentials{}, err
	}

	// With no config file, the environment has to supply everything.
	if cfg == nil {
		if creds.Server == "" || creds.Token == "" {
			return Credentials{}, missingCredentialsError(path, creds)
		}
		return creds, nil
	}

	// Without an explicit server, follow the CLI's current context.
	if creds.Server == "" {
		if cfg.CurrentContext == "" {
			return Credentials{}, fmt.Errorf("%s has no current-context; run 'argocd login <server>' or set %s", path, EnvServer)
		}
		for _, c := range cfg.Contexts {
			if c.Name == cfg.CurrentContext {
				creds.Server = c.Server
				break
			}
		}
		if creds.Server == "" {
			creds.Server = cfg.CurrentContext
		}
	}

	for _, s := range cfg.Servers {
		if s.Server == creds.Server {
			creds.Insecure = s.Insecure
			creds.PlainText = s.PlainText
			break
		}
	}
	if creds.Token == "" {
		for _, u := range cfg.Users {
			if u.Name == creds.Server {
				creds.Token = u.AuthToken
				break
			}
		}
	}

	if creds.Server == "" || creds.Token == "" {
		return Credentials{}, missingCredentialsError(path, creds)
	}
	return creds, nil
}

func loadCLIConfig() (*CLIConfig, string, error) {
	path, err := CLIConfigPath()
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, path, nil
		}
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	var cfg CLIConfig
	// Unknown fields are tolerated here, unlike this CLI's own files: argocd
	// owns this format and adds to it, and rejecting a key argocd wrote would
	// break for reasons the user cannot act on.
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, path, nil
}

func missingCredentialsError(path string, creds Credentials) error {
	switch {
	case creds.Server == "" && creds.Token == "":
		return fmt.Errorf("no ArgoCD server or token: run 'argocd login <server>', or set %s and %s", EnvServer, EnvAuthToken)
	case creds.Server == "":
		return fmt.Errorf("no ArgoCD server: set argocd.server in deploy.yaml, or %s", EnvServer)
	default:
		return fmt.Errorf("no ArgoCD token for %s in %s: run 'argocd login %s', or set %s", creds.Server, path, creds.Server, EnvAuthToken)
	}
}

// ResolveServer applies the precedence for which ArgoCD to talk to: an explicit
// flag, then the environment, then what the repository's deploy.yaml declares.
func ResolveServer(flag, fromDeployFile string) string {
	for _, v := range []string{flag, os.Getenv(EnvServer), fromDeployFile} {
		if v != "" {
			return v
		}
	}
	return ""
}
