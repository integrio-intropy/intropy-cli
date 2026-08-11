---
type: Intropy Playbook
title: Deployment troubleshooting
description: How to diagnose common deployment and promotion failures without bypassing GitOps.
tags: [intropy, playbook, deployment, troubleshooting]
status: draft
commands:
  - intropy deploy pin
  - intropy deploy promote
  - intropy deploy diff
  - intropy deploy sync
  - intropy deploy status
sources:
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
  - id: deploy-help
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/cmd/intropy/deploy.go
    title: deploy command help
  - id: deploy-service
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/deploy/service.go
    title: Deployment service
---

# Use this playbook when

<!-- Describe the failure classes covered here and the evidence to collect before
changing anything. -->

# Safety boundary

Do not repair production state with manual `kubectl` changes. Diagnose the
source, registry, GitOps, or ArgoCD state and make durable corrections through
the owning system.

# Triage map

| Symptom | Likely boundary | Evidence to inspect | Safe next action |
| --- | --- | --- | --- |
| Image for the source commit is missing | <!-- Registry or CI. --> | <!-- Evidence. --> | <!-- Action. --> |
| Source worktree is dirty or HEAD is not pushed | <!-- Source repository. --> | <!-- Evidence. --> | <!-- Action. --> |
| Kustomize pin does not affect rendered manifests | <!-- GitOps base or image name. --> | <!-- Evidence. --> | <!-- Action. --> |
| Promotion source is not healthy at the pin revision | <!-- ArgoCD or source overlay. --> | <!-- Evidence. --> | <!-- Action. --> |
| Concurrent deployment causes a rebase conflict | <!-- GitOps repository. --> | <!-- Evidence. --> | <!-- Action. --> |
| Pending production revision changed after review | <!-- GitOps history. --> | <!-- Evidence. --> | <!-- Action. --> |
| ArgoCD is unreachable or reports a terminal failure | <!-- ArgoCD. --> | <!-- Evidence. --> | <!-- Action. --> |

# Diagnostic route

1. <!-- Identify whether the failure occurred before or after a Git push. -->
2. <!-- Establish the authoritative source for the disputed fact. -->
3. <!-- Compare the requested, committed, and applied revisions. -->
4. <!-- Choose the command that reports or repairs that boundary. -->
5. <!-- Verify the durable state after correction. -->

# Escalation evidence

<!-- List the command, component coordinates, environment, full revision, image
digest, relevant stderr, and ArgoCD operation details needed for escalation.
Exclude credentials and tokens. -->

# Related knowledge

- [State and ownership](/concepts/state-and-ownership.md)
- [Releases and image digests](/concepts/releases-and-image-digests.md)
- [Manual production gate](/workflows/manual-production-gate.md)
