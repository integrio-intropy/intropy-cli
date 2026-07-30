# Why `deploy init` exists and how it lays out a system

Onboarding a component by hand is entirely manual: somebody hand-writes
`component.yaml`, a `base/` and an overlay per environment, for every block,
in every customer repository. `deploy init` does that work, filling in every
fact the CLI can derive — the block's workload, the app-ids that belong in a
Dapr component's `scopes`, the environments `deploy.yaml` defines, the
registry.

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
