# intropy CLI

`intropy` is the command-line interface for working with Intropy integrations
and agent skills. It does two things:

- **Scaffolds integrations** from the official Intropy blueprint library hosted at
  [`integrio-intropy/intropy-blueprints`](https://github.com/integrio-intropy/intropy-blueprints).
- **Manages agent skills** as OCI artifacts — adding, listing, updating, and
  publishing skills (individually or as curated collections) against any OCI
  registry. The skills subsystem implements the
  [Agent Skills OCI Artifacts Spec](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec),
  so artifacts published with `intropy skills publish` interoperate with any
  other spec-compliant tooling.

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

### Install a skill from a collection

```sh
# Register a collection (one-time)
intropy skills collection add --name intropy --ref harbor.intropy.io/skills/index:latest

# Install a skill by name
intropy skills add --name intropy-getting-started

# List what you have installed
intropy skills list
```

## Command overview

```
intropy
├── int                    Manage integrations
│   ├── create <blueprint>     Scaffold a new integration from a blueprint
│   └── describe <blueprint>   Print a blueprint's manifest and parameter schema
├── skills                 Manage Intropy skills
│   ├── add [ref]              Add and install a skill from an OCI registry
│   ├── list                   List installed skills
│   ├── update [name]          Reconcile an installed skill against its collection
│   ├── publish                Publish a skill directory to an OCI registry
│   └── collection             Manage registered skill collections
│       ├── add                    Register a collection in skills.json
│       ├── update <alias>         Refresh or bump a registered collection
│       └── publish <spec> <ref>   Publish a collection as an OCI Image Index
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

Name the integration and scaffold it in one step. `-n/--name` sets the blueprint's
`name` parameter (so you're not prompted for it) and, unless `-o` is given, becomes
the output directory:

```sh
# scaffolds into ./orders and sets name=orders
intropy int create hello-world -n orders

# -o still overrides the output directory
intropy int create hello-world -n orders -o ./my-integration
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

## Skills (`intropy skills`)

Skills are stored as OCI artifacts following the
[Agent Skills OCI Artifacts Spec](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec)
— config schema, layer layout, and annotations all conform to it, so anything
the CLI publishes can be consumed by other spec-compliant clients (and
vice-versa). The CLI maintains two files at the project root:

- `skills.json` — declares registered collections and installed skills (committed).
- `skills.lock.json` — pins resolved digests and install paths (committed).

Skills install into `.agents/skills/<name>/` (the canonical layout from §9 of
the spec). Additional install locations can be configured per skill via
`--also-install-to`.

### Add a skill

By full OCI reference:

```sh
intropy skills add harbor.intropy.io/skills/intropy-pipeline:0.1.0
```

By name, resolved through a registered collection:

```sh
intropy skills add --name intropy-pipeline
intropy skills add --name intropy-pipeline --collection intropy  # disambiguate
```

If no `skills.json` exists in the working directory or any parent, an empty one
is created in the current directory.

### List installed skills

```sh
intropy skills list
intropy skills list -o json   # machine-readable output
```

### Update a skill

`update` reconciles an installed skill against the ref currently pinned by its
collection's cached index. If the collection upstream has been republished, run
`intropy skills collection update <alias>` first to refresh the cache.

```sh
intropy skills update intropy-pipeline
intropy skills update --all
intropy skills update --all -o json   # machine-readable results
```

### Publish a skill

Package a skill directory as an OCI artifact and push it:

```sh
intropy skills publish \
  --path ./skills/intropy-pipeline \
  --ref harbor.intropy.io/skills/intropy-pipeline \
  --tag 0.1.0
```

Use `--force` to overwrite an existing tag, and `--sign` to sign the artifact
with `cosign` after publishing (requires `cosign` on `PATH`).

## Collections

A collection is an OCI Image Index that pins a curated set of skills by digest.
Registering a collection lets you install its skills by name.

### Register a collection

```sh
intropy skills collection add \
  --name intropy \
  --ref harbor.intropy.io/skills/index:latest

intropy skills collection add \
  --name intropy \
  --ref harbor.intropy.io/skills/index:latest \
  -o json   # machine-readable confirmation
```

The collection's index is fetched and cached under
`.intropy/collections/<alias>.json` for offline name lookups.

### Refresh or bump a collection

Re-pull in place (useful when the upstream tag is moving, e.g. `:latest`):

```sh
intropy skills collection update intropy
```

Replace the registered ref with a new value (e.g. bump to a new release tag):

```sh
intropy skills collection update intropy --ref harbor.intropy.io/skills/index:2026.07
```

### Publish a collection

Write a YAML spec listing the skills to include, then publish:

```yaml
# intropy-skills.yaml
name: intropy-skills
description: Curated Intropy skills
skills:
  - ref: harbor.intropy.io/skills/intropy-pipeline:0.1.0
  - ref: harbor.intropy.io/skills/intropy-blocks:0.1.0
```

```sh
intropy skills collection publish intropy-skills.yaml harbor.intropy.io/skills/index:latest
```

Each referenced skill is resolved to its current digest at publish time, so the
collection pins exact content even if upstream tags later move.

## Authentication

OCI operations use the standard Docker credential chain — log in once with
`docker login`, `gh auth login` (for `ghcr.io`), or your registry-specific
tooling, and the CLI will pick up the credentials transparently.

## Project layout

```
cmd/intropy/         Cobra command wiring (one file per command)
internal/blueprint/  Blueprint download, validation, describe, render
internal/skill/      skills.json/lockfile, install/update/add, collection cache
internal/skill/oci/  OCI client wrappers, pack/push/pull, references
```

## Exit codes

- `0` — success
- `1` — runtime error
- `2` — usage error (unknown command, missing required flag, bad argument)

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `intropy int create` fails with "blueprint not found" | The blueprint name is misspelled or does not exist in the library. | Run `intropy int describe <name>` to verify the blueprint exists. Check spelling and case. |
| `intropy skills add` fails with "unauthorized" | Missing or expired registry credentials. | Run `docker login <registry>` or `gh auth login` (for `ghcr.io`) and retry. |
| `intropy skills add --name <skill>` fails with "not found" | The skill name is not in any registered collection, or the collection cache is stale. | Run `intropy skills collection update <alias>` to refresh the cache, or install by full OCI ref. |
| `skills.json` merge conflicts | Multiple contributors edited `skills.json` or `skills.lock.json` simultaneously. | Resolve the conflict manually (both files are plain JSON), then run `intropy skills list` to verify. |
| Windows native errors | Running the Linux binary directly on Windows without WSL. | Use WSL 2 — native Windows is not supported. |

For issues not listed here, run the failing command with `--help` to verify flag usage, or open an issue with the output of `intropy version` and the exact command you ran.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions, code standards,
and the pull request workflow.

## References

- [Agent Skills OCI Artifacts Spec](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec)
  — the packaging, distribution, signing, and tracking spec the `skills`
  subsystem implements.
- [`integrio-intropy/intropy-blueprints`](https://github.com/integrio-intropy/intropy-blueprints)
  — the blueprint library `intropy int create` and `intropy int describe`
  download from by default.
