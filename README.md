# intropy CLI

`intropy` is the command-line interface for working with Intropy integrations.
It **scaffolds integrations** from the official Intropy blueprint library hosted at
[`integrio-intropy/blueprints`](https://github.com/integrio-intropy/blueprints).

## Install

### Homebrew (macOS)

Distributed as a Homebrew cask (macOS only):

```sh
brew tap integrio-intropy/tap
brew trust --tap integrio-intropy/tap
brew install intropy
```

The `brew trust` step is required when `HOMEBREW_REQUIRE_TAP_TRUST` is set —
the default on current Homebrew (6.x+). On older versions without that
requirement you can skip it.

On Linux, use the quick install script below or download a binary from the
[releases page](https://github.com/integrio-intropy/intropy-cli/releases).

### Quick install (macOS / Linux)

```sh
curl -fsSL https://github.com/integrio-intropy/intropy-cli/releases/latest/download/install.sh | sh
```

With options:

```sh
# Install to a custom prefix
curl -fsSL https://github.com/integrio-intropy/intropy-cli/releases/latest/download/install.sh | sh -s -- --prefix ~/.local

# Install a specific version
curl -fsSL https://github.com/integrio-intropy/intropy-cli/releases/latest/download/install.sh | sh -s -- --version v1.0.0
```

The script detects your OS and architecture, downloads the matching release
archive, verifies the SHA256 checksum, optionally verifies the cosign signature,
and installs the binary and shell completions.

### From source

Requires Go 1.26+.

```sh
git clone https://github.com/integrio-intropy/intropy-cli.git
cd intropy-cli
make build
```

Or manually:

```sh
go build -o bin/intropy ./cmd/intropy
```

Add `bin/` to your `PATH`, or move the binary somewhere on your `PATH`.

### Verifying signatures

Release binaries are signed with [cosign](https://sigstore.dev/) using
keyless signing via GitHub Actions OIDC. Each release includes `.sig` and
`.pem` files for every archive.

```sh
# Download the archive, its .sig, and its .pem from the GitHub release
cosign verify-blob \
  --certificate intropy_Darwin_arm64.tar.gz.pem \
  --signature intropy_Darwin_arm64.tar.gz.sig \
  --certificate-identity-regexp="https://github.com/integrio-intropy/intropy-cli" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  intropy_Darwin_arm64.tar.gz
```

### Windows

Windows is not a supported native target. Install and run `intropy` inside
[WSL 2](https://learn.microsoft.com/en-us/windows/wsl/install) using the Linux
instructions above. The CLI relies on Unix path conventions and signal handling
that are not tested on Windows.

### Version stamping

Version, commit, and build date are injected via `-ldflags` at release time:

```sh
go build -ldflags "\
  -X main.version=$(git describe --tags --always) \
  -X main.commit=$(git rev-parse --short HEAD) \
  -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/intropy ./cmd/intropy
```

Check what you have with:

```sh
intropy version
```

## Quickstart

### Scaffold your first integration

```sh
# Inspect a blueprint before you scaffold it
intropy int describe hello-world

# Render it into a new directory
intropy int create hello-world -o ./my-integration
```

## Command overview

```
intropy
├── int                    Manage integrations
│   ├── create <blueprint>     Scaffold a new integration from a blueprint
│   └── describe <blueprint>   Print a blueprint's manifest and parameter schema
└── version                Print version information
```

Run any command with `--help` for full flag documentation.

## Integrations (`intropy int`)

### Describe a blueprint

Inspect what parameters a blueprint accepts before scaffolding it:

```sh
intropy int describe hello-world
intropy int describe hello-world --version v1.2.0
intropy int describe hello-world -o json   # machine-readable; same schema Backstage renders
```

Without `--version`, the latest GitHub release is used.

### Create an integration

```sh
intropy int create hello-world --output ./my-integration
```

Provide parameter values inline, from files, or interactively:

```sh
# inline
intropy int create hello-world -o ./out --set name=orders --set owner=team-x

# from a values file (repeatable; use - for stdin)
intropy int create hello-world -o ./out -f values.yaml

# disable interactive prompts (fail fast on missing required values)
intropy int create hello-world -o ./out --no-input -f values.yaml

# write a machine-readable result document (consumed by chained scaffolders)
intropy int create hello-world -o ./out --output-json result.json
```

Use `--force` to render into a non-empty directory.

## Project layout

```
cmd/intropy/         Cobra command wiring (one file per command)
internal/blueprint/  Blueprint download, validation, describe, render
```

## Exit codes

- `0` — success
- `1` — runtime error
- `2` — usage error (unknown command, missing required flag, bad argument)

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `intropy int create` fails with "blueprint not found" | The blueprint name is misspelled or does not exist in the library. | Run `intropy int describe <name>` to verify the blueprint exists. Check spelling and case. |
| Windows native errors | Running the Linux binary directly on Windows without WSL. | Use WSL 2 — native Windows is not supported. |

For issues not listed here, run the failing command with `--help` to verify flag usage, or open an issue with the output of `intropy version` and the exact command you ran.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions, code standards,
and the pull request workflow.

## References

- [`integrio-intropy/blueprints`](https://github.com/integrio-intropy/blueprints)
  — the blueprint library `intropy int create` and `intropy int describe`
  download from by default.
