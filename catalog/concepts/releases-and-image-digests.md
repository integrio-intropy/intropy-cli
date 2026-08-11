---
type: Intropy Concept
title: Releases and image digests
description: Why releases record immutable image digests and promotion copies them unchanged.
tags: [intropy, concepts, releases, oci, supply-chain]
status: draft
commands:
  - intropy release create
  - intropy release show
  - intropy deploy pin
  - intropy deploy promote
  - intropy deploy status
sources:
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
  - id: release-manifest
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/release/manifest.go
    title: Release manifest model
  - id: promotion-service
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/internal/deploy/promote.go
    title: Deployment promotion behavior
---

# Purpose

<!-- Explain the assurance provided by immutable release manifests and digest
promotion. -->

# Release identity

<!-- Describe the relationship among a component version, source commit, image
names, and image digests. -->

# Pinning versus promotion

| Operation | Resolves from a registry or release | Copies an existing digest | Changes an environment |
| --- | --- | --- | --- |
| Create release | <!-- Fill. --> | <!-- Fill. --> | <!-- Fill. --> |
| Pin | <!-- Fill. --> | <!-- Fill. --> | <!-- Fill. --> |
| Promote | <!-- Fill. --> | <!-- Fill. --> | <!-- Fill. --> |

# Invariants

<!-- Capture the guarantees required for "production runs the bits staging
tested" to remain true. -->

# Failure boundaries

<!-- Describe cases where no fixed digest exists or the source environment
cannot prove that it ran the candidate digest. -->

# Related knowledge

- [Integration to production](/workflows/integration-to-production.md)
- [Manual production gate](/workflows/manual-production-gate.md)
- [Deployment troubleshooting](/playbooks/deployment-troubleshooting.md)
