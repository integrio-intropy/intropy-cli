# Inspecting, rendering, and creating Kubernetes manifests

Manifest generation has three explicit side-effect boundaries:

```text
intropy manifests inspect
intropy manifests render --env local
intropy manifests create --env prod
```

All three derive the same stable deployment model from the system topology and
scaffold records. Extractors become CronJobs; other blocks become Deployments.
The model also resolves app IDs, pub/sub scopes, topics, and connectors.

## Inspect

`manifests inspect` prints the derived model, topology connectors, and the local
fixture catalog from the selected template release. It does not render
manifests, prompt, or touch Git.

Use `-o json` for the machine-readable model.

## Render

`manifests render --env local` renders the local overlay, runs `kustomize build`,
and validates the complete result before returning it. The command writes only
the completed YAML stream to stdout; every progress message, warning, and error
goes to stderr.

This makes the pipe safe:

```sh
intropy manifests render --env local | kubectl apply -f -
```

If topology discovery, template rendering, Kustomize, or final validation fails,
stdout remains empty. The command does not inspect the cluster. Dapr and local
fixture services remain the local cluster setup's responsibility.

The local environment uses the conventional development platform and images.
`--namespace` changes the target namespace. `--image <component>=<name:tag>`
overrides one image; `--image :<tag>` retags every component.

Each topology connector also needs a local fixture binding. Pass repeatable
`--binding <connector>=<fixture>` flags for a reproducible render:

```sh
intropy manifests render --env local \
  --binding fno=http \
  --binding archive=sftp
```

When a binding is omitted and the terminal is interactive, a Huh selector asks
for it. The selector reads from the terminal and writes to stderr, so stdout
remains YAML-only. A non-interactive render fails with the required flag grammar
and available fixtures. Selections apply to one render and are never saved.

## Create

`manifests create --env <environment>` renders the shared base and selected
environment into a refreshed GitOps checkout. It classifies every generated path
before writing:

- a missing file is created;
- a byte-identical file is accepted and left alone, making a repeated run harmless;
- a differing file is a conflict and stops the whole operation;
- no file is ever replaced or deleted.

On success, the command commits only the created paths and pushes
`manifests-create/<domain>-<system>-<environment>` for review. It never pushes
the default branch. A no-op creates no commit.

`--dry-run` reports the action plan without changing Git. `--diff` prints the
current/generated file differences and also writes nothing. Created files carry
no ownership marker: after creation they are ordinary, editable GitOps source.
Image digest pinning remains the responsibility of `intropy deploy pin` and
`intropy deploy promote`. Creation is one-time onboarding for the selected
environment; later structural changes and additional environments are normal,
reviewed GitOps edits.

## Connector bindings

A topology connector names an external edge but does not decide its deployed
Dapr binding. Local rendering receives ephemeral fixture choices through
`--binding` or the interactive selector. GitOps creation always leaves the
binding type, address, credentials, and metadata as explicit `REPLACE-ME`
configuration for maintainers to complete after the review branch is created.
There is no persisted Intropy binding state to drift from those editable GitOps
files.
