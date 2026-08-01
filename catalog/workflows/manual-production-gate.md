---
type: Intropy Workflow
title: Manual production gate
description: How an approver reviews and applies the exact pending revision.
tags: [intropy, workflow, production, argocd, approval]
status: draft
commands:
  - intropy deploy diff
  - intropy deploy sync
sources:
  - id: deploy-diff-command
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/cmd/intropy/deploy_diff.go
    title: deploy diff command
  - id: deploy-sync-command
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/cmd/intropy/deploy_sync.go
    title: deploy sync command
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
---

# Outcome

<!-- Describe the assurance gained by reviewing and syncing one exact Git
revision through ArgoCD. -->

# Preconditions

<!-- Capture the environment policy, pending GitOps change, ArgoCD access, and
RBAC requirements. -->

# Review

<!-- Explain what deploy diff compares, why both sides are committed revisions,
and why ArgoCD performs the rendering. -->

# Apply

<!-- Explain how --revision binds sync to the reviewed pending change and what
happens if the overlay advances. -->

# Warnings that change interpretation

| Warning | Why it matters | Required response |
| --- | --- | --- |
| <!-- Resource removal without prune. --> | <!-- Explain. --> | <!-- Respond. --> |
| <!-- Application is not synced. --> | <!-- Explain. --> | <!-- Respond. --> |
| <!-- ArgoCD is ahead of the pending revision. --> | <!-- Explain. --> | <!-- Respond. --> |

# Audit evidence

<!-- Describe which facts remain in Git and which identity and operation records
come from ArgoCD. -->

# Related knowledge

- [Releases and image digests](/concepts/releases-and-image-digests.md)
- [State and ownership](/concepts/state-and-ownership.md)
- [Deployment troubleshooting](/playbooks/deployment-troubleshooting.md)
