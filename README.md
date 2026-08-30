# intropy CLI

`intropy` is the command-line interface for working with Intropy integrations.
It scaffolds integrations from the official Intropy template library hosted at
[`integrio-intropy/intropy-templates`](https://github.com/integrio-intropy/intropy-templates), then
tracks, deploys, and releases them.

## Install

### Homebrew

Distributed as a Homebrew formula:

```sh
brew tap integrio-intropy/tap
brew trust --tap integrio-intropy/tap
brew install intropy
```

The `brew trust` step is required when `HOMEBREW_REQUIRE_TAP_TRUST` is set —
the default on current Homebrew (6.x+). On older versions without that
requirement you can skip it.

On Linux, use Homebrew, the quick install script below, or download a binary
from the [releases page](https://github.com/integrio-intropy/intropy-cli/releases).

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
intropy template show hello-world

# Render it into a new directory
intropy int create hello-world -o ./my-integration   # -o is --out-dir here
```

## Command overview

```
intropy
├── int                    Manage integrations
│   ├── create <template>      Scaffold a new integration from a template
│   ├── show [dir]             Show the integration scaffolded at a directory
│   └── list [dir]             List scaffolded integrations under a directory
├── template               Inspect the Intropy template library
│   ├── list                   List the templates in the library
│   └── show <template>        Show a template's manifest and parameter schema
├── sys                    Manage integration systems
│   └── create                 Assemble scaffolded integrations into a system host
├── manifests              Inspect, render, and create Kubernetes manifests
│   ├── inspect                Inspect the deployment model derived from a system topology
│   ├── render                 Render a system's manifests as YAML
│   └── create                 Create missing manifests on a GitOps review branch
├── deploy                 Move components between environments via the GitOps repository
│   ├── pin <component> [ver]  Pin a component's image digest into an environment
│   ├── promote <component>    Copy the digests one environment runs into another
│   ├── diff <component>       Show the rendered change a sync would apply
│   ├── status <component>     Show what every environment runs, side by side
│   └── sync <component>       Apply an environment's pending change through ArgoCD
├── release                Publish and inspect immutable release manifests
│   ├── create <component>     Publish a release manifest and push a git tag
│   ├── list <component>       List the releases published for a component
│   └── view <component> <ver> Read a published release manifest
├── context                Manage customer contexts
│   ├── use <name>           Switch the active customer context
│   ├── list                 List customer contexts
│   └── show                 Show the active context's resolved settings
├── dashboard [dir]        Browse the integrations scaffolded under dir
└── version                Print version information
```

Run any command with `--help` for full flag documentation.

## Templates (`intropy template`)

### List templates

See what the template library publishes:

```sh
intropy template list
intropy template list --template-version v1.2.0
intropy template list -o json
```

Without `--template-version`, the latest GitHub release is listed.

### Show a template

Inspect what parameters a template accepts before scaffolding it:

```sh
intropy template show hello-world
intropy template show hello-world --template-version v1.2.0
intropy template show hello-world -o json   # machine-readable; same schema Backstage renders
```

## Integrations (`intropy int`)

### Create an integration

```sh
intropy int create hello-world --out-dir ./my-integration
```

Name the integration and scaffold it in one step. `-n/--name` sets the template's
`name` parameter (so you're not prompted for it) and, unless `-o/--out-dir` is given,
defaults the output directory to the kebab-cased name — `OrderSync` scaffolds into
`./order-sync`, the same normalization `sys create` applies. With neither flag, the
resolved `name` parameter kebab-cases the same way, so a run that answers the prompts
needs no directory decision at all. `-o/--out-dir` always wins when given — the same
split as `dotnet new`, where `-o` is the literal output location and `-n` only names
the artifacts.

> **Note:** in `int create` and `sys create`, `--output json` selects the result document
> on stdout like everywhere else in the CLI. `-o` always means `--out-dir` here.

```sh
# scaffolds into ./orders and sets name=orders (-n OrderSync would scaffold ./order-sync)
intropy int create hello-world -n orders

# -o/--out-dir overrides the output directory: scaffolds into ./order-extractor with name=OrderExtractor
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

# print the machine-readable result document to stdout (same as every other command)
intropy int create hello-world -o ./out --output json
```

Use `--force` to render into a non-empty directory.

`int create` also writes a scaffold record to `.intropy/scaffold.json` inside
the new integration — the template name, the exact release version, and the
resolved parameter values. Commit it: later commands read it to reproduce
decisions made at scaffold time.

### Show an integration

Print the scaffold record of the integration at a directory — which template
rendered it, from which release, with which values:

```sh
# current directory (searched upward)
intropy int show

# an explicit project directory
intropy int show ./my-integration

# the record unchanged, for scripts
intropy int show -o json
```

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
intropy sys create -n OrderFlow -o system-host   # -o is --out-dir here
```

The command reads before it writes: it scans the workspace for the
`.intropy/scaffold.json` records the integration scaffolds left behind,
validates them into a system model, and passes the assembled values to
the `system-host` template (a .NET Aspire AppHost), which renders the
whole declaration — `Topics.cs` defines each topic once as a
`TopicRef<T>`, `Ports.cs` defines each edge block's port to the outside
world (its deployed transport shape — connection values are deployment
configuration), and the `ISystemDefinition` class wires every extractor
and loader to its topic plus its port (`.From(...)` on extractors,
`.To(...)` on loaders) and the platform services (`.Uses(...)`). The workspace's shared contracts project (template role
`shared-library`, typically `Contracts/`) is referenced from the host's
project file, never declared as a component. A contracts project is only
needed when the system has topics: a host of transactional integrations
alone renders with no `Topics.cs` and no contracts reference, and a
topic-bearing system whose workspace lacks one gets it scaffolded by the
host template itself.

The generated development definition (`<Project>Development.cs`) owns the
local-run picture: it mocks the platform services from the skeleton's
OpenAPI documents and resolves each port to a drop folder under the
host's `test/` directory, so the assembled system runs end-to-end with
zero external configuration — drop a file into `test/<name>-source/`,
collect the result from `test/<name>-destination/`.

`-n` accepts PascalCase or kebab-case — `OrderFlow` kebab-cases to
`order-flow`, the system's name. Unlike `int create` there are no `--set`
or values flags: the CLI assembles every value from the scaffold records,
and the template never prompts.

This CLI renders against the current `system-host` release; pin an older
one with `--template-version`.

```sh
# default output directory: the kebab-cased name (./order-flow)
intropy sys create -n OrderFlow

# pin the system-host template release
intropy sys create -n OrderFlow -o system-host --template-version v1.5.0

# machine-readable result document with the assembled model
intropy sys create -n OrderFlow -o system-host --output json
```

> **Note:** as with `int create`, `--output json` prints the result document to stdout.

Records without a `blockKind` (scaffolded by an older CLI) or with an
unsupported block kind are skipped with a warning listing the supported
kinds. The assembled kinds:

- **extractor / loader** — topic blocks. Their records carry `topic`,
  `contract`, and one optional `port`; a record without a `port` value
  keeps its component but gets no `From`/`To`.
- **transactional-integration** — a port-to-port block with no topic. Its
  record carries `fromPort` and `toPort`; both are required, and a missing
  one fails the assembly. The generated system class wires it as
  `.From(Ports.X).To(Ports.Y)`.

Validate the result from the host directory with `dotnet run -- check`.

## Kubernetes manifests (`intropy manifests`)

Manifest generation is split by intent: inspect the derived model, render a
complete YAML stream for local development, or create missing GitOps source
files for one environment.

### Inspect the deployment model

```sh
intropy manifests inspect
intropy manifests inspect --system order-flow -o json
```

`inspect` reads the system topology, scaffold records, and template release. It
reports components, workloads, ports, and the release's available local
fixtures without rendering or writing anything.

### Render local YAML

```sh
intropy manifests render --env local | kubectl apply -f -
intropy manifests render --env local --binding fno=http
```

Each topology port needs a local fixture. Repeat
`--binding <port>=<fixture>` for reproducible and non-interactive renders.
When a choice is missing in an interactive terminal, a Huh selector asks for it;
the selection applies to that render only and is never persisted.

Only YAML is written to stdout. Progress, prompts, warnings, and errors go to
stderr, and the complete render is buffered and validated before stdout is
written. A failed render therefore cannot apply an incomplete prefix through
the pipe. Use `--namespace` to change the target namespace and repeat `--image`
for local image overrides.

### Create GitOps manifests

```sh
intropy manifests create --env prod --dry-run
intropy manifests create --env prod --diff
intropy manifests create --env prod
```

`create` renders one environment and publishes missing files on
`manifests-create/<domain>-<system>-<environment>`, never on the default branch.
Existing byte-identical files are accepted so an interrupted or repeated run
is harmless. A differing existing file is a conflict: manifest creation never
replaces or deletes GitOps source. Once created, the files are ordinary,
editable repository files owned by the GitOps maintainers; onboarding another
environment later is an explicit GitOps edit rather than regeneration.

`--dry-run` reports the file plan. `--diff` prints generated file differences.
Neither mode creates a branch, commit, or push. Repeat
`--binding <port>=<kind>` to select each generated `bindings.<kind>`
type; addresses, credentials and metadata remain `REPLACE-ME` GitOps
configuration for review. These choices are independent of local rendering,
and create never writes local overlays or fixtures to GitOps.

The GitOps path remains `domains/<domain>/<system>/<component>/`. The system
comes from the topology record; `--system` selects a host in a multi-system
workspace. The domain is inferred from the existing GitOps tree or workspace
layout, with `--domain` available on the first run when neither is conclusive.
The latest template release is used by default; `--template-version` pins one.

## Deployment (`intropy deploy pin`)

Pin the image digest that CI built for your current commit into one
environment's kustomize overlay in the GitOps repository.

Run it inside the component's source repository:

```sh
intropy deploy pin order-extractor --env dev --plan
```

`--plan` renders the overlay before and after the change and prints a diff
without writing anything. Drop it to commit and push:

```sh
intropy deploy pin order-extractor --env dev
```

That stages only the overlay's `kustomization.yaml`, commits it with trailers
recording what was deployed, and pushes to the GitOps repository's default
branch.

To ship a published release rather than the current commit, pass its version:

```sh
intropy deploy pin order-extractor 1.4.2 --env staging
```

The release manifest already records the digests, so nothing reads the source
repository — no `HEAD`, no cleanliness check, no registry tag lookup — and the
command works from any directory. Everything after that is identical: the same
plan, diff, commit, push and ArgoCD wait. If someone else pushed first the push is rejected, and the commit is
rebased onto their work and retried — up to five times, with jittered backoff.

A rebase *conflict* means someone deployed the same component to the same
environment in the same moment. That fails loudly and pushes nothing:
auto-resolving would silently pick a winner and discard a deployment someone
believes succeeded. Re-run to deploy on top of theirs.

### What happens

For `intropy deploy pin order-extractor --env dev`, the CLI:

1. Verifies that `git` and `kustomize` are available and resolves the GitOps
   repository from the flag, environment, or user configuration.
2. Locks and refreshes its cached local GitOps checkout, so two local deploys
   cannot edit, reset, or revert the same checkout concurrently.
3. Reads the `dev` and `order-extractor` metadata, then checks that the current
   source checkout is clean for that component and that `HEAD` was pushed. With
   a version given, both checks are skipped: the release already recorded what
   was built, so there is no working tree to vet.
4. Resolves the image digest CI published for that commit — or reads the digests
   the release recorded — temporarily updates the Kustomize overlay, renders it
   before and after, and verifies that the requested image pin reached the
   rendered manifests. If the pipeline has not published the commit's image
   yet, this is where the command fails; pass `--watch` (`-w`) to poll the
   registry until the image appears instead. There is no timeout — like
   `kubectl --watch`, the wait ends when the image arrives or you interrupt
   it.
5. With `--plan`, prints the diff and reverts the temporary edit. Without it,
   commits only the changed `kustomization.yaml` and pushes that commit to the
   GitOps repository's default branch.

After pushing, deploy waits for ArgoCD to apply the new revision and become
healthy. `--no-wait` skips it and `--timeout` bounds it (default 5m).

For an environment with `sync: manual`, deploy commits and stops without
waiting — the gate is in ArgoCD, so it prints the `deploy diff` and `deploy sync`
follow-up, with the revision to review, rather than waiting for a sync that will
never start on its own.

### Are these the bits that were tested?

When the target environment declares `promotesFrom`, the plan also reports what
those environments currently have pinned:

```
orders/order-flow/order-extractor → staging (release 1.4.2, commit 197a3ae)
  harbor.intropy.io/integrations/order-extractor
    :latest → sha256:ad22d6f2ecbc03e79f…
  dev already runs this digest (commit 197a3ae) — you are shipping the tested bits
```

If the digests differ, it says so instead — naming the digest that environment
runs. If it pins a tag rather than a digest, or the component is not onboarded
there, it says there is nothing to compare.

This is reporting, not a gate: a digest an upstream environment never ran still
deploys. Refusing is promotion policy, and belongs to `deploy promote`.

## Promotion (`intropy deploy promote`)

A deploy resolves a digest — from a registry tag, or from a release manifest. A
promotion resolves nothing:

```sh
intropy deploy promote order-extractor --from staging --to prod
```

It reads the digests staging currently pins and writes those exact values into
prod. It does not look `1.4.2` up in the registry, so a release tag that has
since been moved, or a registry that answers differently than it did an hour
ago, cannot substitute different bits for the ones staging tested. That is the
property that lets you say production runs the bytes staging ran.

```
orders/order-flow/order-extractor → prod (release 1.4.2, commit 197a3ae)
  harbor.intropy.io/integrations/order-extractor
    sha256:9f1c0b8ae4d2… → sha256:ad22d6f2ecbc…
  copied from staging, which runs release 1.4.2 (commit 197a3ae)
  orders-order-flow-order-extractor-staging is synced and healthy at 7c30d81

committed 7c30d81 to prod. prod syncs manually — run
'intropy deploy sync order-extractor --env prod' to apply it
```

Everything after the digests are chosen is the deploy path: the same plan and
diff, the same `--plan`, the same rebase-and-retry push, the same ArgoCD wait.
The source environment's `source-commit` and release annotations are copied too
— they answer "which commit produced these bits", and that answer does not
change because the bits moved environments.

### What promotion enforces

Two fields in `deploy.yaml` that a deploy only reports on are enforced here.
Both refuse before anything is written.

**The edge must be declared.** `prod` promoting from `staging` means `dev → prod`
is refused, and the error names the legal sources:

```
error: prod does not promote from dev.
deploy.yaml allows: staging
```

**`requireSourceHealthy` must hold.** The source's ArgoCD application must be
`Synced` and `Healthy` — *and* at the revision its current digests were pinned
by. That second half is the substance. `staging` syncs automatically, so it can
advance between its overlay being read and its health being asked about; a
`Healthy` answer on its own might describe a later deployment of entirely
different bits:

```
error: orders-order-flow-order-extractor-staging is healthy, but at revision
4f8ac21 — not at 7c30d81, which is where its current digests were pinned.
Nothing was written: a healthy application at another revision does not show
that these bits ran
```

A promotion also refuses a source that pins a tag rather than a digest, or pins
nothing at all: there is no fixed set of bits to copy, and inventing one is the
thing promotion exists to avoid.

Unlike a deploy, an unreachable ArgoCD is **fatal** here. A deploy that has
already pushed treats it as a warning, because the commit is the deployment.
For a promotion, health is a precondition — and "I could not check" is not "it
is fine". There is no override flag; clear `requireSourceHealthy` in
`deploy.yaml` if that is really what you want.

## The production gate (`intropy deploy diff`, `intropy deploy sync`)

An environment with `sync: manual` is applied by ArgoCD, not by pushing. A
deploy or a promotion into it records intent and stops. A second person then
reads what it would do, and applies it:

```sh
intropy deploy diff order-extractor --env prod    # review the rendered change
intropy deploy sync order-extractor --env prod    # gated by ArgoCD RBAC
```

### What am I approving? (`intropy deploy diff`)

The diff is between the manifests as they render at the revision ArgoCD has
applied, and at the revision `deploy sync` would apply next:

```
orders/order-flow/order-extractor → prod
  pending  7c30d81  deploy(order-extractor): prod → 1.4.2
           release 1.4.2, promoted from staging, source commit 197a3ae, by robin@example.com
  synced   9a1f4c2  orders-order-flow-order-extractor-prod is OutOfSync and Healthy

--- domains/orders/order-flow/order-extractor/overlays/prod @ 9a1f4c2 (running in prod)
+++ domains/orders/order-flow/order-extractor/overlays/prod @ 7c30d81 (will be applied)
@@ -9,4 +9,4 @@
     spec:
       containers:
         - name: app
-          image: harbor.intropy.io/integrations/order-extractor@sha256:abc123abc123…
+          image: harbor.intropy.io/integrations/order-extractor@sha256:ad22d6f2ecbc…

apply this with:
  intropy deploy sync order-extractor --env prod --revision 7c30d81…
```

This is not `deploy --plan`. A plan diffs a hypothetical uncommitted edit against
the current worktree, for the person writing the change, and holds everything the
overlay refers to constant. Here both sides are commits, so everything between
them counts — a base that moved, several deployments that stacked up unapplied,
an environment that was never synced at all.

**ArgoCD does the rendering.** The Application, not the overlay, is the whole
input: `spec.source.kustomize` overrides and the installation's
`kustomize.buildOptions` are invisible to a local `kustomize build`, and at an
approval gate a diff that is not what gets applied is worse than no diff. ArgoCD
must therefore be reachable — unlike a deploy, where it is an observation and the
commit is the deployment, here its applied revision is an *input*, and a diff
against a guessed baseline is not worth reading. Rendering needs only
`applications, get`, which the approver about to sync already has.

Three things it warns about, because each is a way the diff alone would mislead:

| Situation | Why it matters |
| --- | --- |
| A resource the baseline renders and the pending revision does not | `sync` does not prune, so a resource shown as removed stays in the cluster |
| The application is not `Synced` | Both sides come from git, so a change made with `kubectl` is invisible here — and applying will revert it |
| ArgoCD holds the pending commit or a descendant | Syncing would render the tree as it stood then, reverting what came after |

It also says so when the application renders a path other than the overlay whose
history produced the pending revision, which usually means a hand-edited
Application.

A non-empty diff still exits 0 — this reports, it does not gate. The full sha is
printed rather than the abbreviation, because `--revision` compares by prefix and
an abbreviation only weakens the guard it is handed to. Nothing is written to git,
and `kustomize` is not even required.

### Applying it (`intropy deploy sync`)

The revision synced is the commit that last changed that environment's overlay
— not the branch head. Those are usually the same, but when they are not,
syncing the head would apply commits nobody reviewed. Name the commit whose diff
you actually read and the sync is refused if the pending change is a different
one:

```sh
intropy deploy sync order-extractor --env prod --revision 7c30d81
```

```
error: prod's pending change is 9a1f4c2, not 7c30d81.
domains/orders/order-flow/order-extractor/overlays/prod has advanced since you
reviewed it — read the new diff before syncing
```

Nothing is written to git, and `kubectl` is never invoked. ArgoCD evaluates the
caller through its own RBAC and records who did it, which is why the gate lives
there rather than in a forge-specific approval on a YAML edit — the approver
inspects the rendered resources, in the system that applies them. An
application already holding that revision is reported as a no-op rather than
synced again, and a denied sync fails with ArgoCD's own reason.

### Waiting for ArgoCD

Credentials come from the argocd CLI's own configuration, so if you have run
`argocd login` this needs no extra setup. `ARGOCD_SERVER` and
`ARGOCD_AUTH_TOKEN` override it — those are argocd's variable names, honoured
deliberately so an existing CI setup works unchanged. Precedence for the server
is the `--argocd-server` flag, then `ARGOCD_SERVER`, then `deploy.yaml`, and
finally `argocdServer` in the user configuration — where the active
context's value beats the top-level one. `deploy.yaml` beats the user
configuration on purpose: it travels with the repository the overlays live in.

The wait is defined in terms of the pushed revision, not just sync status.
Polling for `Synced`/`Healthy` alone reads the *previous* revision's perfectly
healthy state on the first poll and reports success before ArgoCD has done
anything. It also accepts a *descendant* of your commit: if another deployment
lands after yours, ArgoCD syncs a later commit and your sha never appears on its
own, so waiting for an exact match would hang forever.

Outcomes:

| Situation | Result |
| --- | --- |
| Applied and healthy | exit 0 |
| Did not converge in time | exit 1, with sync/health status, ArgoCD's operation message, and recent warning events |
| Sync reached a terminal failure | exit 1 immediately, rather than waiting out the timeout |
| ArgoCD unreachable, or the token expired | **exit 0** with a warning — the commit is the deployment, and being unable to watch it does not undo it |
| Interrupted | exit 130 |

A 404 from ArgoCD is most often a wrong `argocd.appNamespace` rather than a
missing application, since Applications are deployed per customer rather than
into the `argocd` namespace. The error says which namespace was tried.

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

A deployment from a release adds `Deploy-Release: 1.4.2` after `Deploy-Env`, and
names the version in the subject instead of the digest — a release is immutable,
so the version identifies those digests exactly:

```
deploy(order-extractor): staging → 1.4.2 (197a3ae)
```

A promotion adds `Deploy-Promoted-From: staging`, and where both versions are
known the subject names the transition instead:

```
deploy(order-extractor): prod 1.4.1 → 1.4.2
```

The prefix stays `deploy(` deliberately: a promotion makes the same edit to the
same file, so a different prefix would split one history in two. The trailer is
what tells them apart.

### Configuration

The GitOps repository is a per-user setting, read from
`~/.config/intropy/config.yaml` (or `$XDG_CONFIG_HOME/intropy/config.yaml`):

```yaml
gitopsRepo: git@gitlab.com:integrio/intropy/customers/acme/gitops.git
```

Override it with `--gitops-repo` or `INTROPY_GITOPS_REPO`; flags win over the
environment, which wins over the file. `git` and `kustomize` must be on `PATH`.

The same file can point scaffolding at a private template library:

```yaml
templateRepo: acme/intropy-templates
```

The value is `owner/repo` on GitHub — the library is fetched over the GitHub
API, so URLs and SSH remotes are rejected. Override it with `--template-repo`
or `INTROPY_TEMPLATE_REPO`, on `int create`, `sys create`, `template list`,
`template show`, and the `manifests` commands. Unset, the official library at
`integrio-intropy/intropy-templates` is used.

#### Customer contexts

Working across several customers means the settings above change together.
The same file holds them as named contexts, kubeconfig-style:

```yaml
organization: integrio
currentContext: acme
contexts:
  acme:
    organization: acme
    gitopsRepo: git@gitlab.com:integrio/intropy/customers/acme/gitops.git
  staging-eu:
    gitopsRepo: git@gitlab.com:integrio/intropy/customers/staging-eu/gitops.git
```

A context overrides only the top-level keys it sets; the rest fall through
to the file's defaults, so `staging-eu` above keeps the top-level
`organization`. Precedence is flag > environment > active context >
top-level keys, and a file with no `contexts:` behaves exactly as before.

Contexts are authored in the file by hand; the CLI switches, lists, and
inspects them:

```sh
intropy context use acme     # persist the active context
intropy context list         # show contexts, marking the active one
intropy context show         # show the resolved settings and where each came from
```

`context show` annotates every value with its source — `env`, `context`, or
`file` — so a forgotten exported variable is visible before a deploy.

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
  staging: { sync: auto, promotesFrom: [dev] }
  prod: { sync: manual, promotesFrom: [staging], requireSourceHealthy: true }
```

`promotesFrom` is the promotion graph: `deploy promote --to prod` accepts only
`--from staging`. `deploy` reads it too, but only to report whether the digests
it is about to pin are what those environments already run.

`sync: manual` means ArgoCD applies the change rather than reconciling it
automatically, so a deploy or promotion into that environment stops after
recording intent and prints the
[`deploy sync`](#the-production-gate-intropy-deploy-sync) follow-up.

Components live at `domains/<domain>/<system>/<component>/`, each with a
`component.yaml` beside `base/` and `overlays/<env>/`:

```yaml
schemaVersion: 1
name: order-extractor
sourcePaths: [integrations/domains/orders/order-flow/order-extractor/]
images:
  - name: harbor.intropy.io/integrations/order-extractor
environments: [dev, staging, prod]
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
intropy deploy pin order-extractor --env dev --plan -o json | jq .appName
```

`deploy diff` is the one command whose diff travels *inside* its JSON, as
`.diff`, so `-o json` turns colour off however it was asked for — ANSI escapes in
a JSON string are not a diff anyone can read.

Exit codes: `0` success or no-op, `1` failure, `2` usage, `127` a required
binary is missing, `130` interrupted. A non-empty diff is not a failure.

### What the overlay records

Alongside the digest, the overlay carries two annotations in
`commonAnnotations`:

```yaml
commonAnnotations:
  deploy.internal/source-commit: 197a3ae981068c375be77cb03e8c85e5ce304612
  deploy.internal/release: 1.4.2
```

`deploy.internal/release` is present only when the digests came from a release,
and a deploy of the current commit **removes** it — a version left beside an
unrelated digest would be read as fact by `deploy promote`, which would then
promote a version that environment never ran. It exists because promotion copies
digests, and a digest does not say which release it belongs to; without it a
promotion could not report `prod 1.4.1 → 1.4.2`.

kustomize propagates common annotations onto pod templates, so a deploy whose
digest is unchanged but whose commit moved still restarts the pods. That is
deliberate — an annotation named `source-commit` that did not track the source
commit would be worse — and the plan says so explicitly when it is the only
change.

## Confirming what is deployed (`intropy deploy status`)

The last step of the release process is checking that the same thing really is
running everywhere:

```sh
intropy deploy status order-extractor
```

```
COMPONENT        ENV      RELEASE  DIGEST               AGE  SYNC    HEALTH
order-extractor  dev      1.4.2    sha256:ad22d6f2ecbc  2h   Synced  Healthy
order-extractor  staging  1.4.2    sha256:ad22d6f2ecbc  47m  Synced  Healthy
order-extractor  prod     1.4.2    sha256:ad22d6f2ecbc  3m   Synced  Healthy

all 3 environments run sha256:ad22d6f2ecbc — these are the same bits, promoted rather than rebuilt
```

The identical digest in every row is the whole point of the design. Promotion
copies digests rather than rebuilding, so agreement here is what makes
"production runs the bits staging tested" a fact instead of a hope — and the
line under the table says whether it holds, so nobody has to compare three
truncated hashes by eye.

When it does not hold, the environments are grouped by what they actually run:

```
the environments do not all run the same bits: dev runs sha256:abc123abc123; staging and prod run sha256:ad22d6f2ecbc
```

Rows are ordered by the promotion graph in `deploy.yaml`, not alphabetically, so
the last row is the furthest downstream. Only the environments the component
declares are shown.

Where each column comes from:

| Column | Source |
| --- | --- |
| `RELEASE` | the overlay's `deploy.internal/release` annotation, or `@<short sha>` from `deploy.internal/source-commit` when it was deployed from a commit |
| `DIGEST` | the digest the overlay pins, in the same form `deploy` and `promote` print |
| `AGE` | the commit that last changed that overlay — ArgoCD reports no timestamp, and the commit is when the change reached the branch |
| `SYNC`, `HEALTH` | ArgoCD's `status.sync.status` and `status.health.status` |

**Nothing here can fail on one bad environment.** An overlay that pins a tag, an
environment the component has never been deployed to, an application ArgoCD does
not know — each is reported as a note under the table and the other rows still
print, because the point of the table is the environments that are fine next to
the one that is not. This is the opposite of `deploy promote`, which *refuses* to
copy out of a tag-pinned overlay; describing one is not the same as promoting it.

If ArgoCD cannot be reached at all, `SYNC` and `HEALTH` are left empty and
everything else still prints with a warning on stderr. Those two columns are the
only ones that come from the cluster; withholding the digests over them would be
withholding most of the answer over part of it.

An environment whose overlay has a committed change ArgoCD has not applied is
called out with the command to review it — for a `sync: manual` environment that
is the normal resting state of an unspent gate, not a fault:

```
prod has a committed change ArgoCD has not applied, waiting on its manual sync gate:
  intropy deploy diff order-extractor --env prod
```

The exit code is **always 0**, even when the environments disagree. This reports;
it does not gate. To gate in CI, read `consistent` from the machine-readable
form, which also carries every image rather than just the one the table has room
for, and the deploy time as an instant rather than a rendered age:

```sh
intropy deploy status order-extractor --output json | jq -e '.consistent'
```

Nothing is written to git, no sync is triggered, `kubectl` is never invoked, and
`kustomize` is not required.

### In the dashboard

`intropy dashboard` shows the same thing per integration, so you can read it
without leaving the catalog:

```sh
intropy dashboard ./integrations
```

Selecting an integration runs `deploy status` for it and renders the environments
as a ladder, in the same promotion order and with the same sentence beneath them.
It is the command's own output — the dashboard decodes it and does not recompute
any of it, so the two cannot disagree about the same overlays.

Two things follow from that being a command rather than a file read:

- **It happens on selection, not on page load.** Reading deployment state
  refreshes the cached GitOps checkout under the same exclusive lock `deploy`
  takes, so the dashboard asks for one integration at a time, reuses the answer,
  and re-runs only when you press Refresh. Nothing polls in the background — a
  browser tab that fetched on a timer would eventually fail your own `deploy`
  with a lock it was holding.
- **Every refusal reaches you verbatim.** An unconfigured `gitopsRepo`, a
  component name that matches several places in the tree, a checkout another
  deploy is using — each is shown as the command worded it, because each is a
  statement about the lookup and not about the integration. None of them renders
  as an empty ladder, which would read as "not deployed".

Sync and health are the only columns that come from ArgoCD. Without a usable
token they are left out entirely and the panel says why, for the same reason the
table leaves them empty rather than guessing: not being able to reach the cluster
says nothing about what the overlays pin.

## Releases (`intropy release`)

A release gives the images built for one source commit a durable version. It
**does not deploy or change any environment**.

### What happens

For `intropy release create component-x --version 1.4.2`, the CLI:

1. Verifies that `git` is available, then locks and refreshes its cached GitOps
   checkout to find `component-x` and read its source paths and images.
2. Checks that the relevant source paths are clean and that the commit to
   release is pushed. The commit is `HEAD`.
3. Resolves the exact image digests CI built for that commit and generates notes
   from the component-scoped commits since the closest ancestor release. If the
   pipeline has not published the
   commit's image yet, this is where the command fails; pass `--watch` (`-w`)
   to poll the registry until the image appears instead. There is no timeout —
   like `kubectl --watch`, the wait ends when the image arrives or you
   interrupt it.
4. Publishes a versioned OCI release manifest containing the component, source
   commit, image digests, notes, and their comparison basis. It does not update
   any GitOps overlay.
5. Creates and pushes the annotated Git tag `component-x/v1.4.2`. A tag-push
   failure is a warning because the OCI manifest is the release; re-running can
   repair a missing tag.

Re-running for a version that already exists compares the intended manifest to
the published one. An identical release is left in place and a missing Git tag
is repaired; a different release is refused because versions are immutable.

Find out what has been released, newest first:

```sh
intropy release list component-x
```

```
VERSION  CREATED     COMMIT   NOTES
1.4.2    2026-07-24  a1b2c3d  Retry on connector timeouts
1.4.1    2026-07-22  9f8e7d6  Handle empty payloads
1.4.0    2026-07-20  4c5d6e7  Initial release.
```

Releases are read from the registry beside the component's images, so this
reports what is actually published rather than what Git tags claim, and the order
is publication order rather than a guess at a versioning scheme. Each row costs
one manifest fetch and no blob transfer, because the version, date, commit, and
first line of the notes are mirrored onto the manifest as annotations. Anything
in that repository that is not a readable release is skipped with a note on
stderr. A component that has never been released
is reported as such rather than treated as an error. `-o json` adds `total` and
each release's full digest.

Inspect a published release before shipping it:

```sh
intropy release show component-x 1.4.2
```

This command changes no source repository, GitOps remote, or environment. It
refreshes the local GitOps cache to locate component metadata, then resolves
that version's OCI manifest and prints its source commit, image digests,
comparison basis, and generated notes. Use `-o json` to receive the manifest
itself. It verifies that the requested OCI tag and the manifest's declared
version agree.

Ship the inspected release with
`intropy deploy pin <component> <version> --env <env>`.

## Authentication

OCI operations use the standard Docker credential chain — log in once with
`docker login`, `gh auth login` (for `ghcr.io`), or your registry-specific
tooling, and the CLI will pick up the credentials transparently.

## Seeding test files from the dashboard

An extractor's local input is a folder: the host's development definition
resolves each external port to one (`development.Files(Ports.ErpSource)
.RootPath("./test/erp-source")`, emitted by the host's `graph --development`
verb), and the running extractor watches it through a localstorage binding.
The flow view can fill that folder for you: every declared extractor carries a
seed action that copies a chosen sample payload into the port's dev folder.

Sample payloads live in a per-system library, keyed by port name — the twin of
the `messages/<port>.md` docs convention:

```
order-flow/
  messages/erp-source.md        # what the payload means
  testdata/erp-source/          # what the payload looks like
    orders-2024-01.csv
    orders-missing-column.csv
```

The library is **committed to git** — it is shared team material, served by any
developer's dashboard — and its samples must be sanitized before committing
them, the same bar the message docs' `redacted: true` assertion sets. Keep it
named `testdata/`: the dev folder itself is conventionally `test/<port>`, and a
library named `test/` would collide with the inbox an extractor consumes from.

The copy lands flat at `<rootPath>/<filename>` — the localstorage binding
reads the folder, not subdirectories. Writing onto a file that is already
there is a conflict the drawer offers to replace; two components consuming the
same port share one inbox, so seeding for one overwrites the other's pending
drop (the conflict prompt is the guard).

A scaffolded-but-undeclared extractor (a ghost) shows the action disabled:
without the declared topology there is no port wiring to seed against. Update
the host, refresh the topology, and the action comes alive.

## Project layout

```
cmd/intropy/          Cobra command wiring (one file per command)

internal/template/    Template download, validation, describe, render
internal/system/      System-host assembly and C# code generation

internal/config/      Per-user configuration (~/.config/intropy/config.yaml)
internal/command/     Runner seam over exec.CommandContext
internal/git/         Typed wrapper over the git binary
internal/kustomize/   kustomize edit/build, manifest normalisation and diffing
internal/registry/    Generic OCI registry client
internal/gitops/      GitOps repository: cached checkout, layout, contract files
internal/source/      Source repo state: commit safety and image digests
internal/deploy/      Deployment orchestration over the packages above
```

The lower group is layered: `command` depends on nothing, `git` and `kustomize`
on `command`, `gitops` on `git` and `config`, `source` on `git`, `gitops` and
`registry`, and `deploy` on all of them. Each generic package is usable on its
own; `deploy` holds the policy that combines them. Test fixtures live in
`internal/gittest`, `internal/gitops/gitopstest` and
`internal/registry/registrytest`.

## Exit codes

These codes are stable and safe to gate scripts and CI on.

- `0` — success
- `1` — runtime error
- `2` — usage error (unknown command, missing required flag, bad argument)
- `127` — a required external binary is missing from `PATH` (`git`, `kustomize`)
- `130` — interrupted (Ctrl-C)

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `intropy int create` fails with "template not found" | The template name is misspelled or does not exist in the library. | Run `intropy template show <name>` to verify the template exists. Check spelling and case. |
| `intropy deploy diff` fails to render a revision | The ArgoCD Application points at a different repository than the one being read, or the history it synced was rewritten. | Check the Application's `spec.source.repoURL` against `gitopsRepo`, and whether the revision it reports still exists. |
| Windows native errors | Running the Linux binary directly on Windows without WSL. | Use WSL 2 — native Windows is not supported. |

For issues not listed here, run the failing command with `--help` to verify flag usage, or open an issue with the output of `intropy version` and the exact command you ran.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions, code standards,
and the pull request workflow.

## References

- [`integrio-intropy/intropy-templates`](https://github.com/integrio-intropy/intropy-templates)
  — the template library `intropy int create` and `intropy template`
  download from by default.

## License

[FSL-1.1-ALv2](LICENSE) © Integrio — Functional Source License, converting to Apache 2.0 two years after each release.
