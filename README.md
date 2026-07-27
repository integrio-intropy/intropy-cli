# intropy CLI

`intropy` is the command-line interface for working with Intropy integrations
and agent skills. It does two things:

- **Scaffolds integrations** from the official Intropy template library hosted at
  [`integrio-intropy/intropy-templates`](https://github.com/integrio-intropy/intropy-templates).
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

### Docker

Multi-arch images (linux/amd64, linux/arm64) are published to GHCR on every
release, built on `distroless/static` and running as a non-root user.

```sh
docker pull ghcr.io/integrio-intropy/intropy-cli:latest

# Scaffolding writes into the working directory: mount your project at /work
# and match your uid so files aren't owned by the container user
docker run --rm -v "$PWD:/work" --user "$(id -u):$(id -g)" \
  ghcr.io/integrio-intropy/intropy-cli int create hello-world --name Orders
```

The image contains no shell — the binary is the entrypoint. Images are signed
with cosign; verify with:

```sh
cosign verify \
  --certificate-identity-regexp="https://github.com/integrio-intropy/intropy-cli" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  ghcr.io/integrio-intropy/intropy-cli:latest
```

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
# Inspect a template before you scaffold it
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
│   ├── create <template>      Scaffold a new integration from a template
│   ├── describe <template>    Print a template's manifest and parameter schema
│   └── list [dir]             List scaffolded integrations under a directory
├── sys                    Manage integration systems
│   └── create                 Assemble scaffolded integrations into a system host
├── deploy <component>     Pin a component's image digest into an environment
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

### Describe a template

Inspect what parameters a template accepts before scaffolding it:

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

Name the integration and scaffold it in one step. `-n/--name` sets the template's
`name` parameter (so you're not prompted for it) and, unless `-o` is given, becomes
the output directory — the same split as `dotnet new`, where `-o` is the literal
output location and `-n` only names the artifacts:

```sh
# scaffolds into ./orders and sets name=orders
intropy int create hello-world -n orders

# -o overrides the output directory: scaffolds into ./order-extractor with name=OrderExtractor
intropy int create hello-world -n OrderExtractor -o ./order-extractor
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

After scaffolding, `int create` offers to install the Intropy agent skills
collection (`harbor.intropy.io/skills/index:latest`) into the new
integration's `.agents/skills/` — a `[Y/n]` prompt in interactive sessions,
skipped under `--no-input` or when stdin is not a terminal. For CI and other
non-interactive runs, `--install-skills` installs without prompting;
`--skip-install-skills` suppresses the prompt and the install entirely:

```sh
intropy int create hello-world -n orders --no-input --install-skills -f values.yaml
intropy int create hello-world -n orders --skip-install-skills
```

Set `INTROPY_SKILLS_COLLECTION` to point the install at a different
collection ref (e.g. a local registry when testing).

`int create` also writes a scaffold record to `.intropy/scaffold.json` inside
the new integration — the template name, the exact release version, and the
resolved parameter values. Commit it: later commands read it to reproduce
decisions made at scaffold time.

### List scaffolded integrations

Discover every integration under a directory tree — each project is
identified by its committed `.intropy/scaffold.json` record:

```sh
# walk down from the current directory
intropy int list

# or from an explicit root
intropy int list ~/dev/integrations

# machine-readable, including the pinned source and scaffold values
intropy int list -o json
```

The walk skips `.git`, `node_modules`, `bin` and `dist`, and doesn't descend
into a project once matched (projects don't nest). Projects with an unreadable
record are reported as warnings on stderr without hiding the rest.

## Systems (`intropy sys`)

### Assemble a system host

Run from the workspace root that holds your scaffolded integrations:

```sh
intropy sys create -n OrderFlow -o system-host
```

The command reads before it writes: it scans the workspace for the
`.intropy/scaffold.json` records the integration scaffolds left behind,
renders the `system-host` template (a .NET Aspire AppHost), and assembles
the typed system declaration from what the scaffolds recorded —
`Topics.cs` defines each topic once as a `TopicRef<T>`, `Connectors.cs`
defines each edge block's port to the outside world, and the
`ISystemDefinition` class wires every extractor and loader to its topic
plus its connector (`.From(...)` on extractors, `.To(...)` on loaders).
The workspace's shared contracts project (template role `shared-library`,
typically `Contracts/`) is referenced from the host, never declared as a
component.

Connectors resolve to local file transports by default: each gets a drop
folder under the host's `test/` directory (created by the command), so
the assembled system runs end-to-end with zero external configuration —
drop a file into `test/<name>-source/`, collect the result from
`test/<name>-destination/`. Point a connector at a real external system
by editing its `ConnectorRef.Define(...)` when transport details are
known.

`-n` accepts PascalCase or kebab-case — `OrderFlow` kebab-cases to
`order-flow`, the system's name. Unlike `int create` there are no `--set`
or values flags: the template renders with only the name, and everything
else comes from the scaffold records.

```sh
# default output directory: the kebab-cased name (./order-flow)
intropy sys create -n OrderFlow

# pin the system-host template release
intropy sys create -n OrderFlow -o system-host --version v1.5.0

# machine-readable result document with the assembled model
intropy sys create -n OrderFlow -o system-host --output-json -
```

Records without a `blockKind` (scaffolded by an older CLI) or with a block
kind other than extractor/loader are skipped with a warning; records
without a `connector` value keep their component but get no `From`/`To`.
Validate the result from the host directory with `dotnet run -- check`.

## Deployment (`intropy deploy`)

Pin the image digest that CI built for your current commit into one
environment's kustomize overlay in the GitOps repository.

Run it inside the component's source repository:

```sh
intropy deploy order-extractor --env dev --plan
```

`--plan` renders the overlay before and after the change and prints a diff
without writing anything. Drop it to commit and push:

```sh
intropy deploy order-extractor --env dev
```

That stages only the overlay's `kustomization.yaml`, commits it with trailers
recording what was deployed, and pushes to the GitOps repository's default
branch. If someone else pushed first the push is rejected, and the commit is
rebased onto their work and retried — up to five times, with jittered backoff.

A rebase *conflict* means someone deployed the same component to the same
environment in the same moment. That fails loudly and pushes nothing:
auto-resolving would silently pick a winner and discard a deployment someone
believes succeeded. Re-run to deploy on top of theirs.

For an environment with `sync: manual`, deploy commits and stops — the gate is
in ArgoCD, so it prints the `deploy sync` follow-up rather than waiting for a
sync that will never start on its own.

### Commit trailers

Each deployment commit carries a machine-readable trailer block, readable with
`git log --format='%(trailers:only=true)'`:

```
deploy(order-extractor): dev → sha256:ad22d6f2ecbc (197a3ae)

Deploy-Component: order-extractor
Deploy-Domain: orders
Deploy-System: order-flow
Deploy-Env: dev
Deploy-Image: harbor.intropy.io/integrations/order-extractor
Deploy-Digest: sha256:ad22d6f2ecbc03e79f…
Deploy-Source-Commit: 197a3ae981068c375be77cb03e8c85e5ce304612
Deploy-By: robin.hultman@integrio.se
Deploy-Cli: intropy-cli/v0.8.0
```

The subject abbreviates the digest so a log stays readable; the trailer carries
it in full. These keys are a format rather than prose — `deploy history` reads
them back — so renaming one is a breaking change.

### Configuration

The GitOps repository is a per-user setting, read from
`~/.config/intropy/config.yaml` (or `$XDG_CONFIG_HOME/intropy/config.yaml`):

```yaml
gitopsRepo: git@gitlab.com:integrio/intropy/customers/acme/gitops.git
```

Override it with `--gitops-repo` or `INTROPY_GITOPS_REPO`; flags win over the
environment, which wins over the file. `git` and `kustomize` must be on `PATH`.

### What the GitOps repository must contain

A `deploy.yaml` at the root marks a repository as deployable:

```yaml
schemaVersion: 1
registry: harbor.intropy.io
argocd:
  server: argocd.intropy.io
  appNamespace: customer-acme
environments:
  dev: { sync: auto }
  prod: { sync: manual, promotesFrom: [dev], requireSourceHealthy: true }
```

Components live at `domains/<domain>/<system>/<component>/`, each with a
`component.yaml` beside `base/` and `overlays/<env>/`:

```yaml
schemaVersion: 1
name: order-extractor
sourcePaths: [integrations/domains/orders/order-flow/order-extractor/]
images:
  - name: harbor.intropy.io/integrations/order-extractor
environments: [dev, prod]
```

The component is found by searching `domains/*/*/<component>`, so you only pass
the name. If it occurs under more than one domain or system the error lists the
candidates and you disambiguate with `--domain` and `--system`.

### What it checks before changing anything

- **The working tree is clean** under the component's `sourcePaths`. CI builds
  the pushed commit, so uncommitted changes mean the thing about to be deployed
  is not the thing you just tested. The check is scoped, so an unrelated dirty
  file elsewhere in a monorepo does not block you; `--allow-dirty` waives it.
- **HEAD is pushed.** An unpushed commit has no image in the registry.
- **The pin actually applies.** `kustomize edit set image` silently adds an
  `images[]` entry that matches nothing, so a pin against an image the base
  never references would leave the render unchanged. That is reported as an
  error rather than as "already at that digest".

Re-running once a digest is already pinned prints `already at …` and exits 0
without creating an empty commit.

### Output

Progress goes to stderr and the diff to stdout, so `--plan` is pipeable. Use
`-o json` for a machine-readable result instead of the diff:

```sh
intropy deploy order-extractor --env dev --plan -o json | jq .appName
```

Exit codes: `0` success or no-op, `1` failure, `2` usage, `127` a required
binary is missing, `130` interrupted.

### A note on the source-commit annotation

Alongside the digest, the overlay records
`deploy.internal/source-commit` in `commonAnnotations`. kustomize propagates
common annotations onto pod templates, so a deploy whose digest is unchanged but
whose commit moved still restarts the pods. That is deliberate — an annotation
named `source-commit` that did not track the source commit would be worse — and
the plan says so explicitly when it is the only change.

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
cmd/intropy/          Cobra command wiring (one file per command)

internal/template/    Template download, validation, describe, render
internal/skill/       skills.json/lockfile, install/update/add, collection cache
internal/skill/oci/    OCI policy wrapper over internal/registry
internal/system/      System-host assembly and C# code generation

internal/config/      Per-user configuration (~/.config/intropy/config.yaml)
internal/command/     Runner seam over exec.CommandContext
internal/git/         Typed wrapper over the git binary
internal/kustomize/   kustomize edit/build, manifest normalisation and diffing
internal/registry/    Generic OCI registry client
internal/gitops/      GitOps repository: cached checkout, layout, contract files
internal/deploy/      Deployment orchestration over the packages above
```

The lower group is layered: `command` depends on nothing, `git` and `kustomize`
on `command`, `gitops` on `git` and `config`, and `deploy` on all of them. Each
generic package is usable on its own; `deploy` holds the policy that combines
them. Test fixtures live in `internal/gittest`, `internal/gitops/gitopstest`
and `internal/registry/registrytest`.

## Exit codes

- `0` — success
- `1` — runtime error
- `2` — usage error (unknown command, missing required flag, bad argument)
- `127` — a required external binary is missing from `PATH` (`git`, `kustomize`)
- `130` — interrupted (Ctrl-C)

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `intropy int create` fails with "template not found" | The template name is misspelled or does not exist in the library. | Run `intropy int describe <name>` to verify the template exists. Check spelling and case. |
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
- [`integrio-intropy/intropy-templates`](https://github.com/integrio-intropy/intropy-templates)
  — the template library `intropy int create` and `intropy int describe`
  download from by default.
