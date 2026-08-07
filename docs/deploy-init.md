# Why `deploy init` exists and how it lays out a system

Onboarding a component by hand is entirely manual: somebody hand-writes
`component.yaml`, a `base/` and an overlay per environment, for every block,
in every customer repository. `deploy init` does that work, filling in every
fact the CLI can derive — the block's workload, the app-ids that belong in a
Dapr component's `scopes`, the environments `deploy.yaml` defines, the
registry.

One pipeline, two destinations:

```
intropy deploy init [component...]    # scaffold the GitOps tree, push a review branch
intropy deploy init --local <system>  # render for the local cluster, stream to stdout
```

Both read the same topology, resolve the same connector question, and render
the same templates. What differs is where the manifests land.

## The connector question

A topology connector carries only its name, external system and directions;
which Dapr binding it deploys as is environment-owned deployment
configuration. `deploy init` asks once per connector per environment and
records the answers in `.intropy/deploy-values.yaml` at the workspace root:

```yaml
connectors:
  erp-orders:
    local: sftp
    staging: sftp
    production: sftp
  crm:
    local: http
```

The file is **checked in**: the whole team scaffolds the same thing, and CI
runs non-interactively. The menu and the validation come from the fetched
library release — `spec.local.fixtures` for the local environment,
`spec.bindings` for every other — so the question, the accepted answers and
the rendered skeletons can never drift apart. When an environment is asked
after another one, the previous answer is the menu default: bindings rarely
differ between GitOps environments, so a re-ask is usually
enter-enter-enter.

Address, hosts and credentials are never asked: they are secrets and
per-environment facts, and they stay `REPLACE-ME` placeholders in the
rendered tree. Under `--no-input` an unbound connector renders exactly that
placeholder scaffold, noted on stderr — except in `--local` mode, where a
fixture must bind and the run fails with a two-line error naming what is
missing. A library release older than `spec.bindings` offers no menu and
gets the same placeholder fallback. An older workspace's
`.intropy/local.yaml` is folded into the state file under `local:` on the
first run and removed.

## Placeholders, not guesses

Complete manifests are not the goal. Connection strings, hosts, credentials
and cron schedules cannot be derived, so they are emitted as
`REPLACE-ME-<HINT>` placeholders and reported as a list when the run
finishes. Filling those in is the developer's remaining job. Image tags are
deliberately not in that list: `intropy deploy` pins digests, and scaffolding
must never pin one.

## The `host` directory and Dapr ownership

A system's shared objects — the Dapr pub/sub and secret store its blocks
resolve by name, and the secrets behind them — go in a `host` directory of
their own. A Dapr Component is namespace-scoped and every integration in the
namespace shares it, so exactly one ArgoCD Application may own each one; the
host directory is what gives them that owner, and `scopes:` is what limits
who may use them.

## Git strategy

Nothing is pushed to the default branch: a tree full of placeholders would be
picked up by the ApplicationSet immediately. The run pushes
`deploy-init/<domain>-<system>` for review instead. Re-running is additive
and safe — a file that already exists and differs is reported and left alone
unless `--force` is given, and `--force` still refuses to overwrite an
overlay that pins a digest.

## The local destination

`--local` fills the gap at the top of the testing ladder:

| Level | Scope | External systems |
|---|---|---|
| unit | one block | mocked in-process |
| integration | the pipeline in one component | mocked/stubbed |
| host | components together | mocked or in-memory |
| **local deploy** | **everything, deployed on a local cluster** | **real protocol, fake peer** |

```
intropy deploy init --local <system> | kubectl apply -f -
```

### The boundary

Three owners, no overlap:

| Owner | Responsibility |
|---|---|
| k3s setup scripts | The cluster, Dapr, and the fixture servers (sftp, smb, http stub) — **always installed**, at stable addresses with conventional dev credentials |
| intropy-templates repo | A `local` overlay on `deploy-host` / `deploy-component`, including the **closed fixture catalog**: one Dapr binding Component per fixture type, pointing at the conventional cluster addresses |
| this CLI | Reads the topology, asks the connector question, renders into a temp dir, builds, streams YAML to stdout |

Because fixtures are always installed, the render is **unconditional** — the
CLI never emits fixture servers, and switching a connector's binding
(`file` → `sftp`) is a one-line edit in the state file and a re-apply. The
emitted manifests are generic: they would apply to any cluster with Dapr and
the fixtures installed.

The fixture contract — namespace, service names, ports, credentials — is
shared between the k3s scripts and the templates' `local` overlay, and
documented once in the scripts repo. It also pins two conventions this
command depends on:

- Component templates render `image: <component>` — a bare name, no registry
  prefix, no tag. The CLI's root kustomization overrides every one to
  `<component>:dev`, and kustomize silently ignores an override that matches
  nothing, so after the build the CLI parses the output and fails if any
  Deployment image lacks a tag.
- The local overlay's skeletons never reference the `.gitops` fields that
  are empty in a local render (`domain`, `argocdAppNamespace`).

### The facts model

A GitOps scaffold derives its facts from a checkout. A local render has no
repository, so the facts are constants of the local cluster the scripts
install:

| Fact | Local source |
|---|---|
| environments | always `["local"]` |
| registry | `"dev"` — a placeholder; every image is overridden at build time |
| platform | provider `kubernetes`, pubsub `redis`, secretStore `kubernetes` |
| domain / ArgoCD app namespace | empty — no local meaning |
| model, scaffolds, selection | the same topology walk as a GitOps scaffold |

The staging tree is flat — `host/`, one directory per component — because
the temp root already holds exactly one system and no commit needs a
repo-relative path.

### Images

Every component image renders as `<component>:dev` — the tag the local build
scripts load images under. The host has no image and gets no `images[]`
entry, the same fact `deploy pin` encodes. Overrides:

- `--image <component>=<name:tag>` — one component
- `--image :<tag>` — every component, the release-candidate flow
  (`--image :1.4.0-rc.3`)

### What a local render deliberately does not do

- No building or loading of container images.
- No installing anything — cluster, Dapr and fixtures are the scripts' job.
- No cluster introspection: the command renders, kubectl applies, and a
  missing cluster fails at apply time with kubectl's own error.
- No `--values`, no `--set`: every fact is derived, and an escape hatch
  would let one render diverge from the team's checked-in state. Flags that
  only mean something against a GitOps repository (`--domain`,
  `--environments`, `--plan`, `--force`, `--gitops-repo`) are rejected with
  `--local`.
- No git writes, no branches, no pushes. The only file written is
  `.intropy/deploy-values.yaml`.
