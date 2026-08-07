# int local — deploy the whole system on a local cluster

The testing ladder has a gap at the top, and this command fills it:

| Level | Scope | External systems |
|---|---|---|
| unit | one block | mocked in-process |
| integration | the pipeline in one component | mocked/stubbed |
| host | components together | mocked or in-memory |
| **local deploy** | **everything, deployed on a local cluster** | **real protocol, fake peer** |

```
intropy int local <system> | kubectl apply -f -
```

One command, manifests on stdout, nothing written except one small state
file.

## The boundary

Three owners, no overlap:

| Owner | Responsibility |
|---|---|
| k3s setup scripts | The cluster, Dapr, and the fixture servers (sftp, smb, http stub) — **always installed**, at stable addresses with conventional dev credentials |
| intropy-templates repo | A `local` overlay on `deploy-host` / `deploy-component`, including the **closed fixture catalog**: one Dapr binding Component per fixture type, pointing at the conventional cluster addresses |
| this CLI | Reads the topology, asks one question per connector, renders into a temp dir, builds, streams YAML to stdout |

Because fixtures are always installed, the render is **unconditional** — the
CLI never emits fixture servers, and switching a connector's binding
(`file` → `sftp`) is a one-line edit in the state file and a re-apply. The
emitted manifests are generic: they would apply to any cluster with Dapr and
the fixtures installed.

The fixture catalog is closed: `sftp`, `smb`, `http`, `file`. A system
needing something exotic hand-writes that Component in its own repo until
the pattern earns a place in the catalog (a template PR, not a CLI release).

The fixture contract — namespace, service names, ports, credentials — is
shared between the k3s scripts and the templates' `local` overlay, and
documented once in the scripts repo. It also pins two conventions this
command depends on:

- Component templates render `image: <component>` — a bare name, no registry
  prefix, no tag. The CLI's root kustomization overrides every one to
  `<component>:dev`, and kustomize silently ignores an override that matches
  nothing, so the guard described below exists.
- The local overlay's skeletons never reference the `.gitops` fields that
  are empty in a local render (`domain`, `argocdAppNamespace`).

## The facts model

`deploy init` derives its facts from a GitOps checkout. A local render has
no repository, so the facts are constants of the local cluster the scripts
install:

| Fact | Local source |
|---|---|
| environments | always `["local"]` |
| registry | `"dev"` — a placeholder; every image is overridden at build time |
| platform | provider `kubernetes`, pubsub `redis`, secretStore `kubernetes` |
| domain / ArgoCD app namespace | empty — no local meaning |
| model, scaffolds, selection | the same topology walk as `deploy init` |

The staging tree is flat — `host/`, one directory per component — because
the temp root already holds exactly one system and no commit needs a
repo-relative path.

## The state file

A topology connector carries only its name, external system and directions;
the binding type is environment-owned deployment configuration. For the
local environment that decision lives in `.intropy/local.yaml` at the
workspace root:

```yaml
connectors:
  erp-orders: sftp
  crm: http
```

The command asks one question per unbound connector — a menu from the
fetched library's `spec.local.fixtures` catalog — and writes the file back
only when an answer changed. The file is **checked in**: the whole team runs
the same local topology, and CI runs non-interactively with `--no-input`,
which fails with a two-line error naming what's missing. A connector the
topology no longer declares leaves a stale entry; harmless, and reported as
a stderr note.

Values are validated against the fetched catalog, so the menu, the
validation and the rendered skeletons always come from one release of one
repo. `--template-version` pins that release, exactly as on `deploy init`;
a library without a catalog is a hard error naming the ref.

## Images

Every component image renders as `<component>:dev` — the tag the local build
scripts load images under. The host has no image and gets no `images[]`
entry, the same fact `deploy pin` encodes. Overrides:

- `--image <component>=<name:tag>` — one component
- `--image :<tag>` — every component, the release-candidate flow
  (`--image :1.4.0-rc.3`)

After the build the CLI parses the output and fails if any Deployment image
lacks a tag: kustomize overrides match on the exact rendered name and
silently ignore misses, so a drifted convention would otherwise ship
unoverridden images with no error until pull time.

## What is deliberately not here

- No building or loading of container images.
- No installing anything — cluster, Dapr and fixtures are the scripts' job.
- No cluster introspection: the command renders, kubectl applies, and a
  missing cluster fails at apply time with kubectl's own error.
- No `--values`, no `--set`: every fact is derived, and an escape hatch
  would let one render diverge from the team's checked-in state.
- No git writes, no branches, no pushes. The only file written is
  `.intropy/local.yaml`.
