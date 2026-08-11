---
type: Intropy Workflow
title: Integration to production
description: The route from a scaffolded integration to a production deployment.
tags: [intropy, workflow, release, deployment]
status: draft
commands:
  - intropy int create
  - intropy sys create
  - intropy deploy init
  - intropy release create
  - intropy deploy pin
  - intropy deploy promote
  - intropy deploy diff
  - intropy deploy sync
  - intropy deploy status
sources:
  - id: cli-readme
    resource: https://github.com/integrio-intropy/intropy-cli/blob/main/README.md
    title: intropy CLI README
---

# Outcome

<!-- State the completed user outcome and the evidence that proves it. -->

# Preconditions

<!-- List only cross-command prerequisites. Link to command help for invocation
requirements. -->

# Route

1. <!-- Scaffold the integration and preserve its scaffold record. -->
2. <!-- Assemble the system topology. -->
3. <!-- Onboard the system into the GitOps repository for review. -->
4. <!-- Publish an immutable release. -->
5. <!-- Pin or promote the release through declared environments. -->
6. <!-- Review and apply environments with a manual sync gate. -->
7. <!-- Confirm deployed digests and health. -->

# Decision points

<!-- Explain when to deploy from a source commit, pin a release, or promote an
already-running digest. -->

# Completion evidence

<!-- Describe the repository, registry, and ArgoCD evidence an agent should
check before reporting success. -->

# Related knowledge

- [Intropy resource model](/concepts/intropy-resource-model.md)
- [Onboard a system](/workflows/onboard-a-system.md)
- [Releases and image digests](/concepts/releases-and-image-digests.md)
- [Manual production gate](/workflows/manual-production-gate.md)
