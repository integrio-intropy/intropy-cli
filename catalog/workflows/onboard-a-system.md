---
type: Intropy Workflow
title: Onboard a system
description: How a system becomes a reviewable GitOps tree.
tags: [intropy, workflow, onboarding, gitops, kustomize]
status: draft
commands:
  - intropy sys create
  - intropy deploy init
sources:
  - id: deploy-init-guide
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/docs/deploy-init.md
    title: Why deploy init exists and how it lays out a system
  - id: deploy-init-command
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/cmd/intropy/deploy_init.go
    title: deploy init command
  - id: deploy-init-service
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/deploy/init.go
    title: deploy init behavior
---

# Outcome

<!-- Describe the reviewable branch and GitOps tree produced by onboarding. -->

# Preconditions

<!-- Capture the required system topology, repository configuration, template
release, and external tools. -->

# Derived values

<!-- Explain what the CLI can safely derive from topology and repository state. -->

# Values requiring operator input

<!-- Explain the placeholder contract and why the CLI does not guess credentials,
hosts, connection strings, or schedules. -->

# Repository layout

<!-- Show a minimal host and component tree. Link to the full repository contract. -->

# Safety properties

<!-- Capture branch behavior, additive reruns, force boundaries, and why unpinned
images remain unpinned. -->

# Review checklist

<!-- Record the checks required before merging the generated GitOps branch. -->

# Related knowledge

- [GitOps repository contract](/contracts/gitops-repository-contract.md)
- [State and ownership](/concepts/state-and-ownership.md)
- [Integration to production](/workflows/integration-to-production.md)
